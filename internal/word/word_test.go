package word

import "testing"

func TestNormalizeAcceptsUnicodeWords(t *testing.T) {
	got, err := Normalize("  silence  ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "silence" {
		t.Fatalf("got %q", got)
	}

	got, err = Normalize("été")
	if err != nil {
		t.Fatal(err)
	}
	if got != "été" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeRejectsNonWords(t *testing.T) {
	cases := []string{"", "two words", "wait.", "hello_world", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	for _, tc := range cases {
		if _, err := Normalize(tc); err == nil {
			t.Fatalf("expected %q to fail", tc)
		}
	}
}
