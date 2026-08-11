package manifest

import "testing"

func TestStoreRoundTrip(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	value := New("11111111-1111-4111-8111-111111111111")
	value.Resources = append(value.Resources, Resource{Kind: "s3-bucket", Name: "lihacloud-bench-1111", Marker: ".lihacloud-bench/owner.json"})
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(value.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunID != value.RunID || len(loaded.Resources) != 1 {
		t.Fatalf("unexpected manifest: %+v", loaded)
	}
}

func TestStoreRejectsDifferentRunID(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	value := New("11111111-1111-4111-8111-111111111111")
	if err := store.Save(value); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("22222222-2222-4222-8222-222222222222"); err == nil {
		t.Fatal("Load() succeeded for unrecorded run ID")
	}
}
