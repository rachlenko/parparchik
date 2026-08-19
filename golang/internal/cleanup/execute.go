package cleanup

import (
	"context"
	"errors"
	"fmt"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/objectstore"
)

// Execute deletes every entry in victims (typically Plan's output) from
// store, removing each from cat only after its storage delete succeeds.
// A single victim's delete failure (e.g. a transient network error) does
// not abort the batch — Execute attempts every victim and returns a
// combined error (via errors.Join) covering all failures, so one bad
// object doesn't block reclaiming the rest of a cleanup run. Call sites
// that need all-or-nothing semantics should check the error and decide
// whether to retry the whole batch; Execute itself does not retry.
func Execute(ctx context.Context, store objectstore.Store, cat *catalog.Catalog, victims []catalog.Entry) error {
	var errs []error
	for _, victim := range victims {
		if err := store.DeleteObject(ctx, victim.Bucket, victim.Key); err != nil {
			errs = append(errs, fmt.Errorf("cleanup: delete %q/%q: %w", victim.Bucket, victim.Key, err))
			continue
		}
		cat.Remove(victim.Key)
	}
	return errors.Join(errs...)
}
