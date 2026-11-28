package tests

import (
	"context"

	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
)

// applyWith applies p and reports whether the item was refused: either the
// whole plan failed to verify, or the item was skipped or failed.
func applyWith(e *env, p model.Plan, ap executor.Approvals) (refused bool) {
	rep, err := executor.Apply(context.Background(), e.cfg, p, ap)
	return err != nil || len(rep.Items) == 0 || rep.Items[0].Outcome != executor.OutcomeDone
}
