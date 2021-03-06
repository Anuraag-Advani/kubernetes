/*
Copyright 2014 The Kubernetes Authors.

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

package cmd

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/rest/fake"
	"k8s.io/kubernetes/pkg/api/legacyscheme"
	api "k8s.io/kubernetes/pkg/apis/core"
	cmdtesting "k8s.io/kubernetes/pkg/kubectl/cmd/testing"
	cmdutil "k8s.io/kubernetes/pkg/kubectl/cmd/util"
	"k8s.io/kubernetes/pkg/kubectl/scheme"
	"k8s.io/kubernetes/pkg/kubectl/util/i18n"
	"k8s.io/kubernetes/pkg/printers"
)

// This init should be removed after switching this command and its tests to user external types.
func init() {
	api.AddToScheme(scheme.Scheme)
}

func TestGetRestartPolicy(t *testing.T) {
	tests := []struct {
		input       string
		interactive bool
		expected    api.RestartPolicy
		expectErr   bool
	}{
		{
			input:    "",
			expected: api.RestartPolicyAlways,
		},
		{
			input:       "",
			interactive: true,
			expected:    api.RestartPolicyOnFailure,
		},
		{
			input:       string(api.RestartPolicyAlways),
			interactive: true,
			expected:    api.RestartPolicyAlways,
		},
		{
			input:       string(api.RestartPolicyNever),
			interactive: true,
			expected:    api.RestartPolicyNever,
		},
		{
			input:    string(api.RestartPolicyAlways),
			expected: api.RestartPolicyAlways,
		},
		{
			input:    string(api.RestartPolicyNever),
			expected: api.RestartPolicyNever,
		},
		{
			input:     "foo",
			expectErr: true,
		},
	}
	for _, test := range tests {
		cmd := &cobra.Command{}
		cmd.Flags().String("restart", "", i18n.T("dummy restart flag)"))
		cmd.Flags().Lookup("restart").Value.Set(test.input)
		policy, err := getRestartPolicy(cmd, test.interactive)
		if test.expectErr && err == nil {
			t.Error("unexpected non-error")
		}
		if !test.expectErr && err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if !test.expectErr && policy != test.expected {
			t.Errorf("expected: %s, saw: %s (%s:%v)", test.expected, policy, test.input, test.interactive)
		}
	}
}

func TestGetEnv(t *testing.T) {
	test := struct {
		input    []string
		expected []string
	}{
		input:    []string{"a=b", "c=d"},
		expected: []string{"a=b", "c=d"},
	}
	cmd := &cobra.Command{}
	cmd.Flags().StringSlice("env", test.input, "")

	envStrings := cmdutil.GetFlagStringSlice(cmd, "env")
	if len(envStrings) != 2 || !reflect.DeepEqual(envStrings, test.expected) {
		t.Errorf("expected: %s, saw: %s", test.expected, envStrings)
	}
}

func TestRunArgsFollowDashRules(t *testing.T) {
	one := int32(1)
	rc := &v1.ReplicationController{
		ObjectMeta: metav1.ObjectMeta{Name: "rc1", Namespace: "test", ResourceVersion: "18"},
		Spec: v1.ReplicationControllerSpec{
			Replicas: &one,
		},
	}

	tests := []struct {
		args          []string
		argsLenAtDash int
		expectError   bool
		name          string
	}{
		{
			args:          []string{},
			argsLenAtDash: -1,
			expectError:   true,
			name:          "empty",
		},
		{
			args:          []string{"foo"},
			argsLenAtDash: -1,
			expectError:   false,
			name:          "no cmd",
		},
		{
			args:          []string{"foo", "sleep"},
			argsLenAtDash: -1,
			expectError:   false,
			name:          "cmd no dash",
		},
		{
			args:          []string{"foo", "sleep"},
			argsLenAtDash: 1,
			expectError:   false,
			name:          "cmd has dash",
		},
		{
			args:          []string{"foo", "sleep"},
			argsLenAtDash: 0,
			expectError:   true,
			name:          "no name",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tf := cmdtesting.NewTestFactory()
			defer tf.Cleanup()

			codec := legacyscheme.Codecs.LegacyCodec(scheme.Versions...)
			ns := legacyscheme.Codecs

			tf.Client = &fake.RESTClient{
				GroupVersion:         schema.GroupVersion{Version: "v1"},
				NegotiatedSerializer: ns,
				Client: fake.CreateHTTPClient(func(req *http.Request) (*http.Response, error) {
					if req.URL.Path == "/namespaces/test/replicationcontrollers" {
						return &http.Response{StatusCode: 201, Header: defaultHeader(), Body: objBody(codec, rc)}, nil
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       ioutil.NopCloser(bytes.NewBuffer([]byte("{}"))),
					}, nil
				}),
			}

			tf.Namespace = "test"
			tf.ClientConfigVal = &restclient.Config{}

			cmd := NewCmdRun(tf, os.Stdin, os.Stdout, os.Stderr)
			cmd.Flags().Set("image", "nginx")
			cmd.Flags().Set("generator", "run/v1")

			printFlags := printers.NewPrintFlags("created")
			printer, err := printFlags.ToPrinter()
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			deleteFlags := NewDeleteFlags("to use to replace the resource.")
			opts := &RunOpts{
				PrintFlags:    printFlags,
				DeleteOptions: deleteFlags.ToOptions(os.Stdout, os.Stderr),

				In:     os.Stdin,
				Out:    os.Stdout,
				ErrOut: os.Stderr,

				Image:     "nginx",
				Generator: "run/v1",

				PrintObj: func(obj runtime.Object) error {
					return printer.PrintObj(obj, os.Stdout)
				},

				ArgsLenAtDash: test.argsLenAtDash,
			}

			err = opts.Run(tf, cmd, test.args)
			if test.expectError && err == nil {
				t.Errorf("unexpected non-error (%s)", test.name)
			}
			if !test.expectError && err != nil {
				t.Errorf("unexpected error: %v (%s)", err, test.name)
			}
		})
	}
}

func TestGenerateService(t *testing.T) {

	tests := []struct {
		port             string
		args             []string
		serviceGenerator string
		params           map[string]interface{}
		expectErr        bool
		name             string
		service          api.Service
		expectPOST       bool
	}{
		{
			port:             "80",
			args:             []string{"foo"},
			serviceGenerator: "service/v2",
			params: map[string]interface{}{
				"name": "foo",
			},
			expectErr: false,
			name:      "basic",
			service: api.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foo",
				},
				Spec: api.ServiceSpec{
					Ports: []api.ServicePort{
						{
							Port:       80,
							Protocol:   "TCP",
							TargetPort: intstr.FromInt(80),
						},
					},
					Selector: map[string]string{
						"run": "foo",
					},
					Type:            api.ServiceTypeClusterIP,
					SessionAffinity: api.ServiceAffinityNone,
				},
			},
			expectPOST: true,
		},
		{
			port:             "80",
			args:             []string{"foo"},
			serviceGenerator: "service/v2",
			params: map[string]interface{}{
				"name":   "foo",
				"labels": "app=bar",
			},
			expectErr: false,
			name:      "custom labels",
			service: api.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "foo",
					Labels: map[string]string{"app": "bar"},
				},
				Spec: api.ServiceSpec{
					Ports: []api.ServicePort{
						{
							Port:       80,
							Protocol:   "TCP",
							TargetPort: intstr.FromInt(80),
						},
					},
					Selector: map[string]string{
						"app": "bar",
					},
					Type:            api.ServiceTypeClusterIP,
					SessionAffinity: api.ServiceAffinityNone,
				},
			},
			expectPOST: true,
		},
		{
			expectErr:  true,
			name:       "missing port",
			expectPOST: false,
		},
		{
			port:             "80",
			args:             []string{"foo"},
			serviceGenerator: "service/v2",
			params: map[string]interface{}{
				"name": "foo",
			},
			expectErr:  false,
			name:       "dry-run",
			expectPOST: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sawPOST := false
			tf := cmdtesting.NewTestFactory()
			defer tf.Cleanup()

			codec := legacyscheme.Codecs.LegacyCodec(scheme.Versions...)
			ns := legacyscheme.Codecs

			tf.ClientConfigVal = defaultClientConfig()
			tf.Client = &fake.RESTClient{
				GroupVersion:         schema.GroupVersion{Version: "v1"},
				NegotiatedSerializer: ns,
				Client: fake.CreateHTTPClient(func(req *http.Request) (*http.Response, error) {
					switch p, m := req.URL.Path, req.Method; {
					case test.expectPOST && m == "POST" && p == "/namespaces/namespace/services":
						sawPOST = true
						body := objBody(codec, &test.service)
						data, err := ioutil.ReadAll(req.Body)
						if err != nil {
							t.Fatalf("unexpected error: %v", err)
						}
						defer req.Body.Close()
						svc := &api.Service{}
						if err := runtime.DecodeInto(codec, data, svc); err != nil {
							t.Fatalf("unexpected error: %v", err)
						}
						// Copy things that are defaulted by the system
						test.service.Annotations = svc.Annotations

						if !reflect.DeepEqual(&test.service, svc) {
							t.Errorf("expected:\n%v\nsaw:\n%v\n", &test.service, svc)
						}
						return &http.Response{StatusCode: 200, Header: defaultHeader(), Body: body}, nil
					default:
						t.Errorf("%s: unexpected request: %s %#v\n%#v", test.name, req.Method, req.URL, req)
						return nil, fmt.Errorf("unexpected request")
					}
				}),
			}

			printFlags := printers.NewPrintFlags("created")
			printer, err := printFlags.ToPrinter()
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			deleteFlags := NewDeleteFlags("to use to replace the resource.")
			buff := &bytes.Buffer{}
			opts := &RunOpts{
				PrintFlags:    printFlags,
				DeleteOptions: deleteFlags.ToOptions(os.Stdout, os.Stderr),

				Out:    buff,
				ErrOut: buff,

				Port:   test.port,
				Record: false,

				PrintObj: func(obj runtime.Object) error {
					return printer.PrintObj(obj, buff)
				},
			}

			cmd := &cobra.Command{}
			cmd.Flags().Bool(cmdutil.ApplyAnnotationsFlag, false, "")
			cmd.Flags().Bool("record", false, "Record current kubectl command in the resource annotation. If set to false, do not record the command. If set to true, record the command. If not set, default to updating the existing annotation value only if one already exists.")
			cmdutil.AddInclude3rdPartyFlags(cmd)
			addRunFlags(cmd)

			if !test.expectPOST {
				opts.DryRun = true
			}

			if len(test.port) > 0 {
				cmd.Flags().Set("port", test.port)
				test.params["port"] = test.port
			}

			_, err = opts.generateService(tf, cmd, test.serviceGenerator, test.params, "namespace")
			if test.expectErr {
				if err == nil {
					t.Error("unexpected non-error")
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if test.expectPOST != sawPOST {
				t.Errorf("expectPost: %v, sawPost: %v", test.expectPOST, sawPOST)
			}
		})
	}
}

func TestRunValidations(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		flags       map[string]string
		expectedErr string
	}{
		{
			name:        "test missing name error",
			expectedErr: "NAME is required",
		},
		{
			name:        "test missing --image error",
			args:        []string{"test"},
			expectedErr: "--image is required",
		},
		{
			name: "test invalid image name error",
			args: []string{"test"},
			flags: map[string]string{
				"image": "#",
			},
			expectedErr: "Invalid image name",
		},
		{
			name: "test stdin replicas value",
			args: []string{"test"},
			flags: map[string]string{
				"image":    "busybox",
				"stdin":    "true",
				"replicas": "2",
			},
			expectedErr: "stdin requires that replicas is 1",
		},
		{
			name: "test rm errors when used on non-attached containers",
			args: []string{"test"},
			flags: map[string]string{
				"image": "busybox",
				"rm":    "true",
			},
			expectedErr: "rm should only be used for attached containers",
		},
		{
			name: "test error on attached containers options",
			args: []string{"test"},
			flags: map[string]string{
				"image":   "busybox",
				"attach":  "true",
				"dry-run": "true",
			},
			expectedErr: "can't be used with attached containers options",
		},
		{
			name: "test error on attached containers options, with value from stdin",
			args: []string{"test"},
			flags: map[string]string{
				"image":   "busybox",
				"stdin":   "true",
				"dry-run": "true",
			},
			expectedErr: "can't be used with attached containers options",
		},
		{
			name: "test error on attached containers options, with value from stdin and tty",
			args: []string{"test"},
			flags: map[string]string{
				"image":   "busybox",
				"tty":     "true",
				"stdin":   "true",
				"dry-run": "true",
			},
			expectedErr: "can't be used with attached containers options",
		},
		{
			name: "test error when tty=true and no stdin provided",
			args: []string{"test"},
			flags: map[string]string{
				"image": "busybox",
				"tty":   "true",
			},
			expectedErr: "stdin is required for containers with -t/--tty",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tf := cmdtesting.NewTestFactory()
			defer tf.Cleanup()

			_, _, codec := cmdtesting.NewExternalScheme()
			tf.Client = &fake.RESTClient{
				NegotiatedSerializer: scheme.Codecs,
				Resp:                 &http.Response{StatusCode: 200, Header: defaultHeader(), Body: objBody(codec, cmdtesting.NewInternalType("", "", ""))},
			}
			tf.Namespace = "test"
			tf.ClientConfigVal = defaultClientConfig()
			inBuf := bytes.NewReader([]byte{})
			outBuf := bytes.NewBuffer([]byte{})
			errBuf := bytes.NewBuffer([]byte{})

			cmd := NewCmdRun(tf, inBuf, outBuf, errBuf)
			for flagName, flagValue := range test.flags {
				cmd.Flags().Set(flagName, flagValue)
			}
			cmd.Run(cmd, test.args)

			var err error
			if errBuf.Len() > 0 {
				err = fmt.Errorf("%v", errBuf.String())
			}
			if err != nil && len(test.expectedErr) > 0 {
				if !strings.Contains(err.Error(), test.expectedErr) {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}

}
