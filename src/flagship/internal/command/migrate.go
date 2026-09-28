package command

import (
	"github.com/flagship-dev/flagship/internal/store"
)

// MigrateResult is the data payload for fs migrate.
type MigrateResult struct {
	Sources []store.MigrateResult `json:"sources"`
	Total   int                   `json:"total_imported"`
	Skipped int                   `json:"total_skipped"`
}

// Migrate imports events from old per-project store.db files into this store.
// Duplicates (same event ID) are skipped. Each source is migrated atomically.
func (h *Handler) Migrate(paths []string) Response {
	if len(paths) == 0 {
		return errResp("no source paths provided")
	}

	var result MigrateResult
	for _, p := range paths {
		r, err := h.store.MigrateFrom(p)
		if err != nil {
			h.logger.Error("migrate failed", "source", p, "error", err)
			return errResp(err.Error())
		}
		h.logger.Info("migrated", "source", p, "imported", r.Imported, "skipped", r.Skipped)
		result.Sources = append(result.Sources, r)
		result.Total += r.Imported
		result.Skipped += r.Skipped
	}

	return okResp(result)
}
