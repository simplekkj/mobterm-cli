package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Stored only on the paired host. This is provenance for a notification, not
// authorization to reconnect, enqueue input, or select a terminal.
type codexRuntimeSource struct {
	Installation string `json:"installation_id"`
	Home         string `json:"home"`
	Socket       string `json:"socket"`
	UID          int    `json:"uid"`
	PeerPID      int    `json:"peer_pid"`
	PeerBirth    string `json:"peer_birth"`
}

func publishUnboundCodexCompletion(ctx context.Context, s Store, api API, installation string, activity Activity, targetErr error) error {
	if activity.Reminder != "turn_completed" || !codexRolloutUUID.MatchString(activity.Session) || !codexRolloutUUID.MatchString(activity.Run) {
		return targetErr
	}
	origin := codexCompletionOrigin(ctx, activity.Session)
	if origin != codexOriginRoot {
		return codexOriginError(origin)
	}
	client, err := codexConnect(ctx)
	if err != nil {
		return targetErr
	}
	defer client.conn.Close()
	// notify is directly spawned by Codex. A shell command that happens to
	// descend from the daemon cannot author a completion by supplying IDs.
	if client.peerPID != os.Getppid() || !codexDaemonImage(ctx, client.peerPID) {
		return errors.New("codex_notification_owner_mismatch")
	}
	config, err := configPath("codex")
	if err != nil {
		return err
	}
	home := filepath.Dir(config)
	physical, err := codexVerifiedSocket(home)
	if err != nil {
		return err
	}
	activity.CodexRoot = true
	activity.CodexRuntime = &codexRuntimeSource{
		Installation: installation, Home: home, Socket: physical,
		UID: os.Geteuid(), PeerPID: client.peerPID, PeerBirth: client.peerBirth,
	}
	// A different CODEX_HOME may have its own copy of the same persistent
	// thread and turn. Distinguish the sources before store/outbox deduplication.
	activity.Instance = digest(home + "\x00" + activity.Session)
	activity.Occurrence = digest(home + "\x00" + activity.Occurrence)
	// Normalize only the append collision domain. Preserve the notification's
	// existing logical identity even when its config entry used a symlink.
	realHome, homeErr := filepath.EvalSymlinks(home)
	var completedAt int64
	if homeErr == nil && realHome == client.home {
		activity.CodexRuntime.Home = realHome
		activity.appendCreated = &completedAt
	}
	confirm := func() error {
		if err := client.confirmCompletionTime(ctx, activity.Session, activity.Run, &completedAt); err != nil {
			return err
		}
		birth, err := processIdentity(client.peerPID)
		if err != nil || birth != client.peerBirth || client.peerPID != os.Getppid() {
			return errors.New("codex_notification_owner_changed")
		}
		return nil
	}
	return publishRegisteredActivity(ctx, s, api, func() error {
		return registerCodexCompletion(ctx, s, activity, confirm)
	})
}

func registerCodexCompletion(ctx context.Context, s Store, activity Activity, confirm func() error) error {
	// Shared daemons spawn notify children independently. Hold the same
	// cross-process lock from the native current-turn read through the
	// durable run/order write. Bucket the private locks to bound files and
	// keep RPC waits outside the general Store lock and network fan-out.
	if err := ensureDir(s.Dir, true); err != nil {
		return err
	}
	bucket := digest(activity.LogicalSession)[:2]
	unlock, err := lockFileWait(ctx, filepath.Join(s.Dir, ".codex-completion-"+bucket+".lock"), true)
	if err != nil {
		return err
	}
	defer unlock()
	if err := confirm(); err != nil {
		return err
	}
	_, _, _, err = applyActivity(ctx, s, Locator{}, activity)
	return err
}

func (c *codexRPC) confirmCompletion(ctx context.Context, thread, turn string) error {
	return c.confirmCompletionTime(ctx, thread, turn, nil)
}

func (c *codexRPC) confirmCompletionTime(ctx context.Context, thread, turn string, completedAt *int64) error {
	result, err := c.request(ctx, "thread/read", map[string]any{"threadId": thread, "includeTurns": false})
	if err != nil {
		return err
	}
	var metadata struct {
		Thread struct {
			ID             string  `json:"id"`
			ParentThreadID *string `json:"parentThreadId"`
			ThreadSource   string  `json:"threadSource"`
			Originator     string  `json:"originator"`
			Ephemeral      *bool   `json:"ephemeral"`
			Status         struct {
				Type string `json:"type"`
			} `json:"status"`
		} `json:"thread"`
	}
	if json.Unmarshal(result, &metadata) != nil || metadata.Thread.ID != thread || metadata.Thread.ParentThreadID != nil ||
		metadata.Thread.ThreadSource != "user" || metadata.Thread.Originator != "codex-tui" || metadata.Thread.Ephemeral == nil || *metadata.Thread.Ephemeral ||
		(metadata.Thread.Status.Type != "idle" && metadata.Thread.Status.Type != "active") {
		return errors.New("codex_completion_thread_unconfirmed")
	}
	// Request no items/text. A delayed callback may refer to a past turn;
	// decline it rather than promoting it ahead of a newer native input.
	result, err = c.request(ctx, "thread/turns/list", map[string]any{"threadId": thread, "limit": 1, "itemsView": "notLoaded", "sortDirection": "desc"})
	if err != nil {
		return err
	}
	var latest struct {
		Data []struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			CompletedAt int64  `json:"completedAt"`
		} `json:"data"`
	}
	if json.Unmarshal(result, &latest) != nil || len(latest.Data) != 1 || latest.Data[0].ID != turn || latest.Data[0].Status != "completed" {
		return errors.New("codex_completion_turn_unconfirmed")
	}
	if completedAt != nil {
		*completedAt = latest.Data[0].CompletedAt
	}
	return nil
}
