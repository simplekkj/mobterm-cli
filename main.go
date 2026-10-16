package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

var version = "dev"
var commit = "unknown"

func printJSON(w io.Writer, v any) { _ = json.NewEncoder(w).Encode(v) }
func flags(name string, args []string, defs func(*flag.FlagSet)) (*flag.FlagSet, error) {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	defs(f)
	return f, f.Parse(args)
}
func status(ctx context.Context, s Store, a API, pairID string, installationOnly bool) (map[string]any, error) {
	id, err := s.installation()
	if err != nil {
		return nil, err
	}
	result := map[string]any{"v": 1, "installation_id": id, "version": version, "commit": commit}
	if installationOnly {
		return result, nil
	}
	result["capabilities"] = map[string]any{"codex": map[string]any{"turn_completed": "agent-turn-complete", "needs_input": false, "reason": "PermissionRequest is pre-decision; no verified external human-wait event"}, "claude": map[string]any{"turn_completed": "native idle_prompt after user prompt; delayed by Claude", "needs_input": []string{"permission_prompt", "elicitation_dialog"}}, "herdr": map[string]any{"versions": []string{"0.9.1", "0.9.3"}, "source": "native Agent hooks only", "protocol": 22}}
	type probeResult struct {
		agent string
		probe agentProbe
	}
	probes := make(chan probeResult, 2)
	for _, agent := range []string{"codex", "claude"} {
		go func(agent string) { probes <- probeResult{agent, probeAgent(ctx, agent)} }(agent)
	}
	for range 2 {
		completed := <-probes
		agent, probe := completed.agent, completed.probe
		capability := result["capabilities"].(map[string]any)[agent].(map[string]any)
		policy := agentCompatibilities[agent]
		capability["version"] = policy.reference
		capability["version_role"] = "validation_reference"
		capability["minimum_version"] = policy.minimum.String()
		capability["supported_range"] = ">=" + policy.minimum.String() + " stable"
		capability["installed"] = probe.installed
		capability["actual_version"] = probe.actual
		capability["supported"] = probe.reason == ""
		if probe.reason != "" {
			capability["version_rejection"] = probe.reason
		}
	}
	installed := map[string]bool{}
	for _, agent := range []string{"codex", "claude"} {
		var r HookRecord
		if s.readJSON("hooks-"+agent+".json", &r) == nil {
			b, e := secureRead(r.Path, 2*1024*1024)
			installed[agent] = e == nil && strings.Contains(string(b), r.Managed)
		}
	}
	result["hooks"] = installed
	pairs, err := s.pairs()
	if err != nil {
		return result, err
	}
	if pairID != "" {
		p, ok := pairs[pairID]
		result["pair_id"] = pairID
		result["local_saved"] = ok
		if !ok {
			result["status"] = "unpaired"
			return result, nil
		}
		remote, err := a.request(ctx, p, "GET", "/v1/pairs/"+pairID, nil)
		if remote != nil {
			for k, v := range remote {
				result[k] = v
			}
		}
		if err != nil {
			var ae *APIError
			if errors.As(err, &ae) && ae.Status == 410 {
				result["status"] = "revoked"
			} else {
				result["status"] = "api_unavailable"
			}
		}
		return result, err
	}
	list := []map[string]any{}
	var firstErr error
	for id, p := range pairs {
		v, e := a.request(ctx, p, "GET", "/v1/pairs/"+id, nil)
		if e != nil {
			if firstErr == nil {
				firstErr = e
			}
			v = map[string]any{"pair_id": id, "status": "api_unavailable"}
			var ae *APIError
			if errors.As(e, &ae) && ae.Status == 410 {
				v["status"] = "revoked"
			}
		}
		v["local_saved"] = true
		list = append(list, v)
	}
	result["pairs"] = list
	if len(list) == 0 {
		result["status"] = "unpaired"
	}
	return result, firstErr
}
func run(args []string, in io.Reader, out, diagnostic io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(out, "mobterm-cli setup --stdin-frame | hooks install|uninstall --agent codex|claude | notify --source codex|claude | test --pair ID --json | status --json [--pair ID] | status --installation-json | target verify --stdin-frame | upgrade --version vX.Y.Z | version")
		return 0
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Fprintf(out, "mobterm-cli %s %s %s/%s\n", version, commit, platformOS, platformArch)
		return 0
	}
	s, err := defaultStore()
	if err != nil {
		fmt.Fprintln(diagnostic, "mobterm-cli: configuration unavailable")
		return 1
	}
	base, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	timeout := 10 * time.Second
	if args[0] == "upgrade" {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(base, timeout)
	defer cancel()
	a := newAPI()
	var value any
	switch args[0] {
	case "setup":
		var framed bool
		f, e := flags("setup", args[1:], func(f *flag.FlagSet) { f.BoolVar(&framed, "stdin-frame", false, "") })
		if e != nil || !framed || f.NArg() != 0 {
			err = errors.New("setup requires --stdin-frame")
		} else {
			value, err = setup(ctx, s, a, in)
		}
	case "target":
		if len(args) != 3 || args[1] != "verify" || args[2] != "--stdin-frame" {
			err = errors.New("target verify requires --stdin-frame")
		} else {
			value, err = verifyTarget(ctx, s, in)
		}
	case "status":
		var pair string
		var j, installation bool
		f, e := flags("status", args[1:], func(f *flag.FlagSet) {
			f.StringVar(&pair, "pair", "", "")
			f.BoolVar(&j, "json", false, "")
			f.BoolVar(&installation, "installation-json", false, "")
		})
		if e != nil || f.NArg() != 0 {
			err = errors.New("invalid status arguments")
		} else {
			value, err = status(ctx, s, a, pair, installation)
		}
	case "test":
		var pair string
		var j bool
		f, e := flags("test", args[1:], func(f *flag.FlagSet) { f.StringVar(&pair, "pair", "", ""); f.BoolVar(&j, "json", false, "") })
		if e != nil || f.NArg() != 0 {
			err = errors.New("invalid test arguments")
		} else {
			var results []map[string]any
			results, err = notify(ctx, s, a, "", nil, pair, true)
			if pair != "" && len(results) == 1 {
				value = results[0]
			} else {
				value = map[string]any{"v": 1, "results": results}
			}
		}
	case "hooks":
		if len(args) < 2 {
			err = errors.New("hooks requires install or uninstall")
			break
		}
		var agent string
		var j bool
		f, e := flags("hooks", args[2:], func(f *flag.FlagSet) { f.StringVar(&agent, "agent", "", ""); f.BoolVar(&j, "json", false, "") })
		if e != nil || f.NArg() != 0 {
			err = errors.New("invalid hook arguments")
			break
		}
		executable, e := os.Executable()
		if e != nil {
			err = e
			break
		}
		err = changeHooks(ctx, s, agent, args[1], "", executable, true)
		value = map[string]any{"v": 1, "agent": agent, "action": args[1], "ok": err == nil}
	case "notify":
		var source string
		var chain bool
		f, e := flags("notify", args[1:], func(f *flag.FlagSet) {
			f.StringVar(&source, "source", "", "")
			f.BoolVar(&chain, "mobterm-chain", false, "")
		})
		var payload []byte
		if e != nil {
			err = e
		} else if source == "codex" && f.NArg() == 1 {
			payload = []byte(f.Arg(0))
		} else if source == "claude" && f.NArg() == 0 {
			payload, err = readHook(in)
		} else {
			err = errors.New("invalid hook invocation")
		}
		if err == nil {
			_, err = notify(ctx, s, a, source, payload, "", false)
		}
		if err != nil {
			fmt.Fprintln(diagnostic, "mobterm-cli: notification unavailable (agent continues)")
		}
		if chain && source == "codex" && len(payload) > 0 {
			return runNotifyChain(s, string(payload))
		}
		return 0
	case "upgrade":
		var target string
		f, e := flags("upgrade", args[1:], func(f *flag.FlagSet) { f.StringVar(&target, "version", "", "") })
		if e != nil || f.NArg() != 0 {
			err = errors.New("invalid upgrade arguments")
			break
		}
		dest, e := os.Executable()
		if e != nil {
			err = e
			break
		}
		err = upgrade(ctx, target, dest)
		value = map[string]any{"version": target, "upgraded": err == nil}
	default:
		err = errors.New("unknown command")
	}
	if value != nil {
		printJSON(out, value)
	}
	if err != nil {
		fmt.Fprintln(diagnostic, "mobterm-cli:", err)
		if value == nil {
			printJSON(out, map[string]any{"v": 1, "ok": false, "error": "operation_failed"})
		}
		return 1
	}
	return 0
}
func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
