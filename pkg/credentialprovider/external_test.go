/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package credentialprovider

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/credentialprovider/apis/registrycredentials"
	"k8s.io/kubernetes/pkg/credentialprovider/apis/registrycredentials/v1alpha1"
	utilexec "k8s.io/utils/exec"
	fakeexec "k8s.io/utils/exec/testing"
)

var (
	ada = AuthConfig{
		Username: "ada",
		Password: "smash",
	}
	grace = AuthConfig{
		Username: "grace",
		Password: "squash",
	}
)

func TestConfigDeserialization(t *testing.T) {
	tests := []struct {
		configYaml    []byte
		expectedObj   *registrycredentials.RegistryCredentialConfig
		matchExpected bool
	}{
		{
			configYaml: []byte(`apiVersion: registrycredentials.k8s.io/v1alpha1
kind: RegistryCredentialConfig
providers:
-
  imageMatchers:
  - "*.dkr.ecr.*.amazonaws.com"
  - "*.dkr.ecr.*.amazonaws.com.cn"
  exec:
    command: ecr-creds
    args:
    - token
    env:
    - name: XYZ
      value: envvalue
    apiVersion: registrycredentials.k8s.io/v1alpha1`),
			expectedObj: &registrycredentials.RegistryCredentialConfig{
				Providers: []registrycredentials.RegistryCredentialProvider{
					registrycredentials.RegistryCredentialProvider{
						ImageMatchers: []string{
							"*.dkr.ecr.*.amazonaws.com",
							"*.dkr.ecr.*.amazonaws.com.cn",
						},
						Exec: registrycredentials.ExecConfig{
							Command: "ecr-creds",
							Args:    []string{"token"},
							Env: []registrycredentials.ExecEnvVar{
								registrycredentials.ExecEnvVar{
									Name:  "XYZ",
									Value: "envvalue",
								},
							},
						},
					},
				},
			},
			matchExpected: true,
		},
	}
	for _, test := range tests {
		actualObj, err := decode(test.configYaml)
		if err != nil {
			t.Errorf("Decode failed with error: %s", err.Error())
		}

		if diff := cmp.Diff(test.expectedObj, actualObj); diff != "" {
			t.Errorf("Unexpected diff (-want +got):\n%s", diff)
		}
	}
}

// TestExternalProviderKeyringLookup is based on TestDockerKeyringLookup and
// has almost exactly the same test cases, the difference being that this
// external Lookup is expected to only return one set of creds
func TestExternalProviderKeyringLookup(t *testing.T) {
	fakeCmd, fakeExec := newFakeExternalCredentialProviderExecer()

	keyring := &externalProviderKeyring{
		providers: map[string]ExternalCredentialProvider{
			"bar.example.com": &externalCredentialProvider{
				command: "",
				args:    []string{},
				env:     []string{},
				execer:  fakeExec,
			},
		},
		index: []string{"bar.example.com"},
	}

	tests := []struct {
		name  string
		image string
		match []AuthConfig
		ok    bool
	}{
		{
			"direct match",
			"bar.example.com",
			[]AuthConfig{ada},
			true,
		},
		{
			"direct match",
			"bar.example.com/pong",
			[]AuthConfig{grace},
			true,
		},
		{
			"no direct match, deeper path ignored",
			"bar.example.com/ping",
			[]AuthConfig{ada},
			true,
		},
		{
			"match first part of path token",
			"bar.example.com/pongz",
			[]AuthConfig{grace},
			true,
		},
		{
			"match regardless of sub-path",
			"bar.example.com/pong/pang",
			[]AuthConfig{grace},
			true,
		},
		{
			"no host match",
			"example.com",
			[]AuthConfig{},
			false,
		},
		{
			"no host match",
			"foo.example.com",
			[]AuthConfig{},
			false,
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			match, ok := keyring.Lookup(tt.image)
			if tt.ok != ok {
				t.Errorf("case %d: expected ok=%t, got %t", i, tt.ok, ok)
			}

			if diff := cmp.Diff(tt.match, match); diff != "" {
				t.Errorf("case %d: unexpected diff (-want +got):\n%s", i, diff)
			}

			// Restart at the beginning of the Run/Command scripts otherwise they run
			// out of actions
			fakeCmd.RunCalls = 0
			fakeExec.CommandCalls = 0
		})
	}
}

// newFakeExternalCredentialProviderExecer returns a fake exec that provides:
// - grace's credentials for images containing "bar.example.com/pong"
// - ada's credentials for images containing "bar.example.com"
func newFakeExternalCredentialProviderExecer() (*fakeexec.FakeCmd, *fakeexec.FakeExec) {
	fakeCmd := &fakeexec.FakeCmd{}
	fakeCmd.RunScript = []fakeexec.FakeAction{
		func() ([]byte, []byte, error) {
			buf := new(bytes.Buffer)
			buf.ReadFrom(fakeCmd.Stdin)

			var request v1alpha1.RegistryCredentialPluginRequest
			if err := runtime.DecodeInto(codecs.UniversalDecoder(v1alpha1.SchemeGroupVersion), buf.Bytes(), &request); err != nil {
				panic(err)
			}

			if strings.Contains(request.Image, "bar.example.com/pong") {
				return []byte(encodeResponseOrDie(grace.Username, grace.Password)), nil, nil
			} else if strings.Contains(request.Image, "bar.example.com") {
				return []byte(encodeResponseOrDie(ada.Username, ada.Password)), nil, nil
			}

			return buf.Bytes(), nil, nil
		},
	}

	fakeExec := &fakeexec.FakeExec{}
	fakeExec.CommandScript = []fakeexec.FakeCommandAction{
		func(cmd string, args ...string) utilexec.Cmd {
			return fakeexec.InitFakeCmd(fakeCmd, cmd, args...)
		},
	}

	return fakeCmd, fakeExec
}

func encodeResponseOrDie(username, password string) string {
	encoder, err := newRegistryCredentialEncoder(v1alpha1.SchemeGroupVersion)
	if err != nil {
		panic(err)
	}
	response := v1alpha1.RegistryCredentialPluginResponse{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "registrycredentials.k8s.io/v1alpha1",
			Kind:       "RegistryCredentialConfig",
		},
		Username: &username,
		Password: &password,
	}
	return runtime.EncodeOrDie(encoder, &response)
}
