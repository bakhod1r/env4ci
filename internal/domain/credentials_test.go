package domain

import (
	"reflect"
	"testing"
)

const fakeKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\n-----END OPENSSH PRIVATE KEY-----"

func vars(kv ...string) []Variable {
	var out []Variable
	for i := 0; i < len(kv); i += 2 {
		out = append(out, Variable{Key: kv[i], Value: kv[i+1]})
	}
	return out
}

func TestDetectSSH(t *testing.T) {
	cases := []struct {
		name string
		in   []Variable
		want []SSHCredential
	}{
		{
			name: "generic SSH_* names",
			in:   vars("SSH_PRIVATE_KEY", fakeKey, "SSH_HOST", "h", "SSH_USER", "u", "SSH_PORT", "22", "SSH_KNOWN_HOSTS", "k"),
			want: []SSHCredential{{Key: "SSH_PRIVATE_KEY", Host: "SSH_HOST", User: "SSH_USER", Port: "SSH_PORT", KnownHosts: "SSH_KNOWN_HOSTS"}},
		},
		{
			name: "prefixed names win over generic",
			in:   vars("DEPLOY_SSH_KEY", fakeKey, "DEPLOY_HOST", "h", "SSH_HOST", "other", "DEPLOY_SSH_USER", "u", "DEPLOY_PASSPHRASE", "p"),
			want: []SSHCredential{{Key: "DEPLOY_SSH_KEY", Host: "DEPLOY_HOST", User: "DEPLOY_SSH_USER", Passphrase: "DEPLOY_PASSPHRASE"}},
		},
		{
			name: "key without host: parse-only",
			in:   vars("GIT_DEPLOY_KEY", fakeKey),
			want: []SSHCredential{{Key: "GIT_DEPLOY_KEY"}},
		},
		{
			name: "values that are not keys are ignored",
			in:   vars("API_KEY", "abc123", "SSH_HOST", "h"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DetectSSH(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestDetectRegistry(t *testing.T) {
	got := DetectRegistry(vars(
		"GHCR_TOKEN", "t", "GHCR_USER", "u",
		"DOCKERHUB_TOKEN", "t", "DOCKERHUB_USERNAME", "u",
		"REGISTRY", "registry.corp.io", "REGISTRY_USER", "u", "REGISTRY_PASSWORD", "p",
	))
	want := []RegistryCredential{
		{Registry: "ghcr.io", Username: "GHCR_USER", Password: "GHCR_TOKEN"},
		{Registry: "docker.io", Username: "DOCKERHUB_USERNAME", Password: "DOCKERHUB_TOKEN"},
		{Registry: "registry.corp.io", Username: "REGISTRY_USER", Password: "REGISTRY_PASSWORD"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
	if got := DetectRegistry(vars("APP_PORT", "1")); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestCredentialKeys(t *testing.T) {
	c := SSHCredential{Key: "K", Host: "H", User: "U"}
	if !reflect.DeepEqual(c.Keys(), []string{"H", "K", "U"}) {
		t.Fatalf("%v", c.Keys())
	}
}

func TestDetectSelfHostedRegistries(t *testing.T) {
	got := DetectRegistry(vars(
		"HARBOR_REGISTRY", "harbor.corp.io", "HARBOR_REGISTRY_USER", "robot", "HARBOR_REGISTRY_TOKEN", "t",
		"GHE_REGISTRY", "containers.ghe.corp", "GHE_USERNAME", "me", "GHE_TOKEN", "t",
		"DOCKER_REGISTRY", "registry.local:5000", "DOCKER_USERNAME", "u", "DOCKER_PASSWORD", "p",
		"EMPTY_REGISTRY", "", "NOPASS_REGISTRY", "x.io",
	))
	want := []RegistryCredential{
		{Registry: "registry.local:5000", Username: "DOCKER_USERNAME", Password: "DOCKER_PASSWORD"},
		{Registry: "containers.ghe.corp", Username: "GHE_USERNAME", Password: "GHE_TOKEN"},
		{Registry: "harbor.corp.io", Username: "HARBOR_REGISTRY_USER", Password: "HARBOR_REGISTRY_TOKEN"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}
