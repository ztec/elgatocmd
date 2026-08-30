package buildinfo

import (
	"strings"
	"testing"
)

func TestDevelopmentIdentityIsComplete(t *testing.T) {
	for name, value := range map[string]string{
		"name": Name, "description": Description, "repository": RepositoryURL,
		"release API": ReleaseAPI, "signing-key registry API": SigningKeyRegistryAPI, "version": Version,
	} {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("%s must not be empty", name)
		}
	}
	if !strings.HasPrefix(RepositoryURL, "https://") || !strings.HasPrefix(ReleaseAPI, "https://") || !strings.HasPrefix(SigningKeyRegistryAPI, "https://") {
		t.Fatal("release identity must use HTTPS")
	}
	if SigningKeyRegistryAPI != "https://git2.riper.fr/api/v1/repos/ztec/tmplt" {
		t.Fatal("generated repositories must retain Tmplt as the central signing-key registry")
	}
}

func TestDevelopmentIdentityMatchesTemplateAnswers(t *testing.T) {
	if Name != "Elgato Key Light Neo USB controller" {
		t.Fatalf("name = %q", Name)
	}
	if Description != "Control Elgato Key Light Neo devices over Linux USB and bridge them to Home Assistant." {
		t.Fatalf("description = %q", Description)
	}
}
