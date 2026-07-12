// Package preference re-exports the store.PreferenceStore as a thin adapter.
// This keeps the package name in the directory consistent with the project layout
// while the actual SQL logic lives in internal/store.
package preference

import "notification-service/internal/store"

// Store is an alias for store.PreferenceStore.
// Other packages can import preference.Store instead of store.PreferenceStore.
type Store = store.PreferenceStore

// New is an alias for store.NewPreferenceStore.
func New(db *store.DB) *Store {
	return store.NewPreferenceStore(db)
}
