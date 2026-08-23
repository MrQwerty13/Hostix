package cli

import "testing"

func TestParsePortBindings(t *testing.T) {
	bindings, err := parsePortBindings([]string{"8000:8000", "5353:53/udp"})
	if err != nil {
		t.Fatalf("parsePortBindings() error = %v", err)
	}
	if got, want := len(bindings), 2; got != want {
		t.Fatalf("len(bindings) = %d, want %d", got, want)
	}
	if bindings[1].Protocol != "udp" || bindings[1].ContainerPort != 53 {
		t.Fatalf("UDP binding = %#v", bindings[1])
	}
}

func TestParsePortBindingsRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"8000", "0:80", "80:70000", "80:80/sctp", "127.0.0.1:80:80"} {
		t.Run(value, func(t *testing.T) {
			if _, err := parsePortBindings([]string{value}); err == nil {
				t.Fatalf("parsePortBindings(%q) unexpectedly succeeded", value)
			}
		})
	}
}

func TestParseEnvironment(t *testing.T) {
	environment, err := parseEnvironment([]string{"PORT=8000", "TOKEN=a=b", "PORT=9000"})
	if err != nil {
		t.Fatalf("parseEnvironment() error = %v", err)
	}
	if got, want := environment["PORT"], "9000"; got != want {
		t.Fatalf("PORT = %q, want %q", got, want)
	}
	if got, want := environment["TOKEN"], "a=b"; got != want {
		t.Fatalf("TOKEN = %q, want %q", got, want)
	}
}

func TestParseEnvironmentRejectsInvalidNames(t *testing.T) {
	for _, value := range []string{"PORT", "=value", "1PORT=value", "BAD-NAME=value"} {
		t.Run(value, func(t *testing.T) {
			if _, err := parseEnvironment([]string{value}); err == nil {
				t.Fatalf("parseEnvironment(%q) unexpectedly succeeded", value)
			}
		})
	}
}
