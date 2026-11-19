package core

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

// Requestable versions (ADR-0046): an item offers every published version,
// its default the newest.

// Requestable says whether a request may name the version: one of the
// item's published versions.
func Requestable(it store.CatalogItem, versionID string) bool {
	return slices.ContainsFunc(it.Requestable, func(v store.VersionRef) bool { return v.ID == versionID })
}

// requestable is the version a request names, when it is requestable.
// Another is refused as stale, and the request evaluated on the item's
// default, so that its other errors show.
func (s *Service) requestable(ctx context.Context, it store.CatalogItem, versionID string,
	add func(field, code, msg string)) (store.ItemVersion, error) {
	if Requestable(it, versionID) {
		return s.Store.GetVersion(ctx, versionID)
	}
	msg := "This form is out of date. Reload it to get the current version."
	v, err := s.Store.GetVersion(ctx, versionID)
	switch {
	case err == nil && v.ItemID == it.ID && v.State != "draft":
		msg = "Version " + v.Number + " of " + it.Name + " is no longer offered: request " + deref(it.VersionNumber) + "."
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return store.ItemVersion{}, err
	}
	add("/version_id", "stale_version", msg)
	return s.Store.GetVersion(ctx, *it.PublishedVersionID)
}

// holdVersion keeps the version a request or an update names from being
// retired until tx ends: one retired since it was checked is refused as
// stale, as the form would be.
func (s *Service) holdVersion(ctx context.Context, tx pgx.Tx, id string) error {
	ok, err := s.Store.LockVersionPublished(ctx, tx, id)
	if err != nil || ok {
		return err
	}
	return &ValidationError{Errors: []api.FieldError{fieldErr("/version_id", "stale_version",
		"This version was retired meanwhile. Reload the form to get the current version.")}}
}
