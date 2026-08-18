package format

import "testing"

type stubFormat struct{ name string }

func (s stubFormat) Name() string { return s.name }
func (s stubFormat) Route(bucket, key string) string {
	return "/" + s.name + "/" + bucket + "/" + key
}
func (s stubFormat) ParseRoute(route string) (bucket, key string, ok bool) {
	return "", "", false
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	// Arrange
	r := NewRegistry()
	f := stubFormat{name: "generic"}

	// Act
	r.Register(f)
	got, ok := r.Get("generic")

	// Assert
	if !ok {
		t.Fatal("Get(generic) ok = false, want true")
	}
	if got.Name() != "generic" {
		t.Errorf("Get(generic).Name() = %q, want generic", got.Name())
	}
}

func TestRegistry_GetUnknown(t *testing.T) {
	// Arrange
	r := NewRegistry()

	// Act
	_, ok := r.Get("maven")

	// Assert
	if ok {
		t.Error("Get(maven) ok = true, want false — nothing registered yet")
	}
}
