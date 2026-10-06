package naming

import (
	"reflect"
	"testing"
)

func TestWordsAndPolicies(t *testing.T) {
	for input, want := range map[string][]string{
		"httpURLPort": {"http", "url", "port"}, "ipv4Addr": {"ipv4", "addr"}, "userID": {"user", "id"},
		"__hello--WORLD name\t": {"hello", "world", "name"}, "ÄpfelURLPort": {"äpfel", "url", "port"}, "": {},
	} {
		if got := Words(input); !reflect.DeepEqual(got, want) {
			t.Errorf("Words(%q)=%v, want %v", input, got, want)
		}
	}
	for policy, want := range map[string]string{"Camel": "httpUrlPort", "Pascal": "HttpUrlPort", "Snake": "http_url_port", "Kebab": "http-url-port", "ScreamingSnake": "HTTP_URL_PORT"} {
		words := []string{"http", "url", "port"}
		if got := JoinWords(words, policy); got != want {
			t.Errorf("%s=%q, want %q", policy, got, want)
		}
		if words[1] != "url" {
			t.Fatal("join changed its input")
		}
	}
}

func TestYAMLStringNames(t *testing.T) {
	for _, name := range []string{"On", "ON", "off", "yes", "SiteAdmin", "0xGG"} {
		if !YAMLString(name) {
			t.Errorf("%q should resolve as a string", name)
		}
	}
	for _, name := range []string{"true", "TRUE", "False", "NULL", "~", "42", "-12", "1e3", ".inf", ".NaN", "0xFF", "0o77", "a: b", "[a]", "#tag", " ", "a\n---\nb"} {
		if YAMLString(name) {
			t.Errorf("%q should not resolve as a string", name)
		}
	}
}
