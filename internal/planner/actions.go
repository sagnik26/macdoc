package planner

import "github.com/sagnik26/macdoc/internal/model"

// ActionKind is what apply does for one item.
type ActionKind string

// The kinds of action a plan item carries.
const (
	Native     ActionKind = "native"     // run the tool's own command
	Quarantine ActionKind = "quarantine" // move the folder into macdoc's quarantine
	Sweep      ActionKind = "sweep"      // delete the .DS_Store files below a folder (ADR 0014)
	System     ActionKind = "system"     // remove a system item with administrator rights (ADR 0014)
)

// Action is one step of a plan, in the order apply performs them. The
// dry-run view and the executor both come from Actions, so what the plan
// shows is what apply does.
type Action struct {
	Item   model.PlanItem
	Kind   ActionKind
	Argv   []string // Native
	Target string   // Quarantine, Sweep
}

// Actions lists a plan's steps in plan order.
func Actions(p model.Plan) []Action {
	out := make([]Action, 0, len(p.Items))
	for _, it := range p.Items {
		switch it.Method {
		case model.MethodNative:
			out = append(out, Action{Item: it, Kind: Native, Argv: append([]string(nil), it.Argv...)})
		case model.MethodQuarantine:
			out = append(out, Action{Item: it, Kind: Quarantine, Target: it.Target})
		case model.MethodSweep:
			out = append(out, Action{Item: it, Kind: Sweep, Target: it.Target})
		case model.MethodSystem:
			out = append(out, Action{Item: it, Kind: System, Target: it.Target})
		}
	}
	return out
}

// Performer carries out actions. The Phase 6 executor implements it, after
// re-validating each item; tests use a recording one.
type Performer interface {
	RunNative(item model.PlanItem, argv []string) error
	Quarantine(item model.PlanItem, target string) error
	Sweep(item model.PlanItem, root string) error
	System(item model.PlanItem, target string) error
}

// Result is the outcome of one action.
type Result struct {
	Action Action
	Err    error
}

// Perform runs every action in order with p. A failed item doesn't stop the
// others (docs/SAFETY_MODEL.md §4.3 step 6).
func Perform(plan model.Plan, p Performer) []Result {
	acts := Actions(plan)
	out := make([]Result, len(acts))
	for i, a := range acts {
		var err error
		switch a.Kind {
		case Native:
			err = p.RunNative(a.Item, a.Argv)
		case Quarantine:
			err = p.Quarantine(a.Item, a.Target)
		case Sweep:
			err = p.Sweep(a.Item, a.Target)
		case System:
			err = p.System(a.Item, a.Target)
		}
		out[i] = Result{Action: a, Err: err}
	}
	return out
}
