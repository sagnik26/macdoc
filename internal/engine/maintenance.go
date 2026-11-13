package engine

import (
	"context"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// Maintenance lists the maintenance steps (recipes marked on_request) that
// apply to this Mac. They are planned by ID like any other suggestion; the
// usual suggestions leave them out. It only reads.
func Maintenance(ctx context.Context, o OpportunityOptions) (OpportunityResult, error) {
	if o.Catalog == nil {
		cat, err := recipes.Load()
		if err != nil {
			return OpportunityResult{}, err
		}
		o.Catalog = cat
	}
	o.OnRequest, o.MinBytes = true, -1
	res, err := Opportunities(ctx, o)
	if err != nil {
		return res, err
	}
	kept := []model.Opportunity{}
	for _, op := range res.Opportunities {
		if r, ok := o.Catalog.Get(op.RecipeID); ok && r.OnRequest {
			kept = append(kept, op)
		}
	}
	res.Opportunities = kept
	res.Totals = map[model.Risk]int64{}
	return res, nil
}
