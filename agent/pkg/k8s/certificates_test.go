package k8s

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/linkerd/linkerd2/pkg/identity"
	ldConsts "github.com/linkerd/linkerd2/pkg/k8s"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestFindIdentityPod(t *testing.T) {
	fixtures := []*struct {
		testName    string
		pods        []runtime.Object
		expectedErr error
	}{
		{
			"can find identity pod",
			[]runtime.Object{
				&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "linkerd-identity",
						Namespace: "linkerd",
						Labels: map[string]string{
							ldConsts.ControllerComponentLabel: identityComponentName,
						},
					},
					Status: v1.PodStatus{
						Phase: v1.PodRunning,
					},
				},
				&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "linkerd-destination",
						Namespace: "linkerd",
					},
				},
			},
			nil,
		},
		{
			"cannot find a running identitiy pod",
			[]runtime.Object{
				&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "linkerd-identity",
						Namespace: "linkerd",
						Labels: map[string]string{
							ldConsts.ControllerComponentLabel: identityComponentName,
						},
					},
					Status: v1.PodStatus{
						Phase: v1.PodPending,
					},
				},
				&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "linkerd-destination",
						Namespace: "linkerd",
					},
				},
			},
			errors.New("could not find running pod for linkerd-identity"),
		},
		{
			"cannot find a running identitiy pod",
			[]runtime.Object{
				&v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "linkerd-destination",
						Namespace: "linkerd",
					},
				},
			},
			errors.New("could not find linkerd-identity pod"),
		},
	}

	for _, tc := range fixtures {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			c := fakeClient(tc.pods...)
			c.Sync(nil, time.Second)
			client := NewClient(c.sharedInformers)

			pod, err := client.getControlPlaneComponentPod(identityComponentName)
			if tc.expectedErr != nil {
				if tc.expectedErr.Error() != err.Error() {
					t.Fatalf("exepected err %s, got %s", tc.expectedErr, err)
				}
			} else {
				if pod.Name != "linkerd-identity" {
					t.Fatalf("exepected pod with name linkerd-identity, got %s", pod.Name)
				}
			}
		})
	}
}

func TestGetProxyContainer(t *testing.T) {
	fixtures := []*struct {
		testName    string
		pod         *v1.Pod
		expectedErr error
	}{
		{
			"meshed pod",
			&v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "linkerd-identity",
					Namespace: "linkerd",
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name: ldConsts.ProxyContainerName,
						},
						{
							Name: "some-other-container",
						},
					},
				},
			},
			nil,
		},
		{
			"unmeshed pod",
			&v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "linkerd-identity",
					Namespace: "linkerd",
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name: "some-other-container",
						},
					},
				},
			},
			errors.New("could not find proxy container in pod linkerd/linkerd-identity"),
		},
	}

	for _, tc := range fixtures {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			container, err := getProxyContainer(tc.pod)
			if tc.expectedErr != nil {
				if tc.expectedErr.Error() != err.Error() {
					t.Fatalf("exepected err %s, got %s", tc.expectedErr, err)
				}
			} else {
				if container.Name != ldConsts.ProxyContainerName {
					t.Fatalf("exepected container with name %s, got %s", ldConsts.ProxyContainerName, container.Name)
				}
			}
		})
	}
}

func TestGetAdminPort(t *testing.T) {
	fixtures := []*struct {
		testName     string
		container    *v1.Container
		expectedPort int32
		expectedErr  error
	}{
		{
			"container with admin port",
			&v1.Container{
				Name: ldConsts.ProxyContainerName,
				Ports: []v1.ContainerPort{
					{
						Name:          ldConsts.ProxyAdminPortName,
						ContainerPort: 555,
					},
					{
						Name:          "another port",
						ContainerPort: 444,
					},
				},
			},
			555,
			nil,
		},
		{
			"container without admin port",
			&v1.Container{
				Name: ldConsts.ProxyContainerName,
				Ports: []v1.ContainerPort{
					{
						Name:          "another port",
						ContainerPort: 444,
					},
				},
			},
			0,
			fmt.Errorf("could not find port linkerd-admin on proxy container [linkerd-proxy]"),
		},
	}

	for _, tc := range fixtures {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			port, err := getProxyAdminPort(tc.container)
			if tc.expectedErr != nil {
				if tc.expectedErr.Error() != err.Error() {
					t.Fatalf("exepected err %s, got %s", tc.expectedErr, err)
				}
			} else {
				if port != tc.expectedPort {
					t.Fatalf("exepected port %d, got %d", tc.expectedPort, port)
				}
			}
		})
	}
}

func TestGetServerName(t *testing.T) {
	podSa := "some-sa"
	podNs := "some-ns"

	fixtures := []*struct {
		testName     string
		container    *v1.Container
		expectedName string
		expectedErr  error
	}{
		{
			"gets correct name",
			&v1.Container{
				Name: ldConsts.ProxyContainerName,
				Env: []v1.EnvVar{
					{
						Name:  linkerdNsEnvVarName,
						Value: "linkerd",
					},
					{
						Name:  linkerdTrustDomainEnvVarName,
						Value: "cluster.local",
					},
				},
			},
			fmt.Sprintf("%s.%s.serviceaccount.identity.linkerd.cluster.local", podSa, podNs),
			nil,
		},
		{
			"missing ns env var",
			&v1.Container{
				Name: ldConsts.ProxyContainerName,
				Env: []v1.EnvVar{
					{
						Name:  linkerdTrustDomainEnvVarName,
						Value: "cluster.local",
					},
				},
			},
			"",
			fmt.Errorf("could not find %s env var on proxy container [%s]", linkerdNsEnvVarName, ldConsts.ProxyContainerName),
		},
		{
			"missing trust domain env var",
			&v1.Container{
				Name: ldConsts.ProxyContainerName,
				Env: []v1.EnvVar{
					{
						Name:  linkerdNsEnvVarName,
						Value: "linkerd",
					},
				},
			},
			"",
			fmt.Errorf("could not find %s env var on proxy container [%s]", linkerdTrustDomainEnvVarName, ldConsts.ProxyContainerName),
		},
	}

	for _, tc := range fixtures {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			name, err := getServerName(podSa, podNs, tc.container)
			if tc.expectedErr != nil {
				if tc.expectedErr.Error() != err.Error() {
					t.Fatalf("exepected err %s, got %s", tc.expectedErr, err)
				}
			} else {
				if name != tc.expectedName {
					t.Fatalf("exepected name %s, got %s", tc.expectedName, name)
				}
			}
		})
	}
}

func TestExtractRootCerts(t *testing.T) {
	roots := `-----BEGIN CERTIFICATE-----
	MIIBiDCCAS6gAwIBAgIBATAKBggqhkjOPQQDAjAcMRowGAYDVQQDExFpZGVudGl0
	eS5saW5rZXJkLjAeFw0yMTA1MjUwODMxMjNaFw0yMjA1MjUwODMxNDNaMBwxGjAY
	BgNVBAMTEWlkZW50aXR5LmxpbmtlcmQuMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcD
	QgAEMUWGUJK3hCWLnSFqqVAEZDrpJdcgqOR8N6HCwUZ5W/xUaaKG6mJ4jqXb6A/V
	smasJzHS1hvuq8X5hUladbJPwqNhMF8wDgYDVR0PAQH/BAQDAgEGMB0GA1UdJQQW
	MBQGCCsGAQUFBwMBBggrBgEFBQcDAjAPBgNVHRMBAf8EBTADAQH/MB0GA1UdDgQW
	BBR2rObnHBdmbV6DsgK/WUz7GjB91TAKBggqhkjOPQQDAgNIADBFAiAgaebof4VK
	BOXnfrTiBdBWxeBCpVa+eLVOqGFgtWN/YQIhAI0FrCU0HkMvKuL/dquRKMqorWie
	xW1kfPch6RizdaxS
	-----END CERTIFICATE-----
	`

	fixtures := []*struct {
		testName      string
		container     *v1.Container
		expectedCerts string
		expectedErr   error
	}{
		{
			"gets correct cert",
			&v1.Container{
				Name: ldConsts.ProxyContainerName,
				Env: []v1.EnvVar{
					{
						Name:  identity.EnvTrustAnchors,
						Value: roots,
					},
				},
			},
			roots,
			nil,
		},
		{
			"no roots",
			&v1.Container{
				Name: ldConsts.ProxyContainerName,
				Env:  []v1.EnvVar{},
			},
			"",
			fmt.Errorf("could not find env var with name %s on proxy container [linkerd-proxy]", identity.EnvTrustAnchors),
		},
	}

	for _, tc := range fixtures {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			roots, err := extractRootsCerts(tc.container)
			if tc.expectedErr != nil {
				if tc.expectedErr.Error() != err.Error() {
					t.Fatalf("exepected err %s, got %s", tc.expectedErr, err)
				}
			} else {
				rootsString := string(roots.Raw)
				if rootsString != tc.expectedCerts {
					t.Fatalf("exepected roots %s, got %s", tc.expectedCerts, rootsString)
				}
			}
		})
	}
}
