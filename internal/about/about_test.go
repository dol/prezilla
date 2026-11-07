package about

import "testing"

func TestNewDefaultsVersionToDev(t *testing.T) {
	if got := New("").Version; got != "dev" {
		t.Fatalf("Version = %q, want dev", got)
	}
	if got := New("1.2.3"); got.Version != "1.2.3" || got.Name != Name || got.IconName != IconName {
		t.Fatalf("unexpected info %+v", got)
	}
}

func TestTextExactLines(t *testing.T) {
	want := "Prezilla 1.2.3\nPresentation mode switcher\nLicense: Apache-2.0\nWebsite: https://github.com/dol/prezilla\n"
	if got := New("1.2.3").Text(); got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
}

func TestDisplayNameIsPrezilla(t *testing.T) {
	if Name != "Prezilla" {
		t.Fatalf("Name = %q, want Prezilla", Name)
	}
	if IconName != "prezilla" {
		t.Fatalf("IconName = %q, want lowercase identifier prezilla", IconName)
	}
}
