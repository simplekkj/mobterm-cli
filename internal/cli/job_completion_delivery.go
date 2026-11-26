package cli

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// The worker invokes this once, after committing the encrypted result and terminal fact.
// Only an already sealed outbox entry may be retried later by the normal delivery path.
func deliverQuickJobCompletion(ctx context.Context, s Store, api API, pairID, eventID string, completion QuickCommandCompletion) string {
	ctx, cancel := context.WithTimeout(withDeliveryRetries(ctx), 35*time.Second)
	defer cancel()

	pairs, err := s.pairs()
	pair, ok := pairs[pairID]
	if err != nil || !ok || pair.Status != "active" || pair.InstallationID != completion.InstallationID {
		return "pair_unavailable"
	}
	response, err := api.pairQuery(ctx, s, pair, "GET", "/v2/pairs/"+pairID+"/capabilities", nil)
	if err != nil {
		return "policy_unavailable"
	}
	b, err := json.Marshal(response)
	var policy quickCommandPolicy
	if err != nil || json.Unmarshal(b, &policy) != nil {
		return "policy_unavailable"
	}
	d, err := s.queueQuickJobCompletion(ctx, pair, eventID, policy, completion)
	if err != nil {
		if err.Error() == "quick_command_notification_unavailable" {
			return "policy_unavailable"
		}
		return "outbox_unconfirmed"
	}
	result, _ := deliverOne(ctx, s, api, d)
	if result["outbox_handoff"] == true {
		return "api_handoff"
	}
	if state, ok := result["state"].(string); ok && terminalDelivery(state) {
		return state
	}
	return "outbox_saved"
}

func (s Store) queueQuickJobCompletion(ctx context.Context, pair Pair, eventID string, policy quickCommandPolicy, completion QuickCommandCompletion) (delivery, error) {
	unlock, err := s.lock(ctx)
	if err != nil {
		return delivery{}, err
	}
	defer unlock()
	pairs, err := s.pairs()
	if err != nil || pairs[pair.PairID] != pair {
		return delivery{}, errors.New("quick_command_notification_unavailable")
	}
	if err := s.migrateOutbox(); err != nil {
		return delivery{}, err
	}
	key := deliveryKey(pair.PairID, eventID)
	sh, err := s.loadShard(key)
	if err != nil {
		return delivery{}, err
	}
	// The local fingerprint distinguishes attempted reuse of an event ID. No
	// command text, output, navigation locator, or agent grant enters the outbox.
	b, err := json.Marshal(completion)
	if err != nil {
		return delivery{}, err
	}
	fingerprint := digest(string(b))
	if d, ok := sh.Records[key]; ok {
		if d.Kind != quickCommandCompleted || d.CompletionHash != fingerprint {
			return delivery{}, errors.New("event_identity_conflict")
		}
		return d, nil
	}
	now := time.Now().Unix()
	envelope, err := sealQuickCommand(pair, pair.PairID, eventID, policy, completion, now, nil)
	if err != nil {
		return delivery{}, err
	}
	old := cloneShard(sh)
	pruneShard(&sh, now)
	d := delivery{PairID: pair.PairID, EventID: eventID, Kind: quickCommandCompleted,
		CompletionHash: fingerprint, Created: now, Expires: envelope.ExpiresAt, State: "pending", Envelope: &envelope}
	return s.insertDeliveryLocked(ctx, key, old, sh, d)
}
