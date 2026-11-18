package resource

import "testing"

// FuzzDecode checks that no document panics the decoder and that what it accepts is named and kinded.
func FuzzDecode(f *testing.F) {
	f.Add([]byte(workloadDoc))
	f.Add([]byte("apiVersion: karga.io/v1alpha1\nkind: Volume\nmetadata:\n  name: data\n"))
	f.Add([]byte("apiVersion: karga.io/v1alpha1\nkind: Secret\nmetadata:\n  name: s\nspec:\n  files:\n    a: b\n"))
	f.Add([]byte("kind: [\n"))
	f.Fuzz(func(t *testing.T, doc []byte) {
		r, err := Decode(doc)
		if err != nil {
			return
		}
		if r.Kind == "" || !IsDNSLabel(r.Metadata.Name) || r.Spec == nil {
			t.Fatalf("accepted %q as kind %q name %q spec %v", doc, r.Kind, r.Metadata.Name, r.Spec)
		}
	})
}
