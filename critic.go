package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// critic records the reflexion critic's judgment on a hypothesis verdict. It is asymmetric by
// design: a reject can only DOWNGRADE a PROVED to need_more_data (catching behavior changes or
// benchmark-gaming the numeric gate missed); it can never promote a rejection. This keeps the
// numeric gate authoritative for wins while letting a distinct pass veto false positives.
type criticCmd struct {
	ID     string `arg:"" help:"hypothesis id"`
	Reject bool   `help:"the critic rejected the change (downgrades a PROVED to need_more_data)"`
	Reason string `help:"the critic's reason (required with --reject)"`
}

func (c *criticCmd) Run() error {
	if c.Reject && c.Reason == "" {
		return fmt.Errorf("--reject requires --reason")
	}
	path := filepath.Join(gpaDir, "runs", c.ID, "verdict.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no verdict for %s; run bench-verdict first", c.ID)
	}
	var v Verdict
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	v.Critic = &CriticReview{Passed: !c.Reject, Reason: c.Reason}

	switch {
	case c.Reject && v.Status == "proved":
		old := ""
		if v.Verdict != nil {
			old = v.Verdict.Reason
			v.Verdict.Reason = "downgraded by critic: " + c.Reason + " (gate said: " + old + ")"
		}
		v.Status = "need_more_data"
		info("critic rejected %s: PROVED -> need_more_data (%s)", c.ID, c.Reason)
	case c.Reject:
		info("critic note on %s (status %s, unchanged): %s", c.ID, v.Status, c.Reason)
	default:
		info("critic passed %s", c.ID)
	}
	return writeJSON(path, v)
}
