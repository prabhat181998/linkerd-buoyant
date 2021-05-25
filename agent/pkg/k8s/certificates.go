package k8s

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"time"

	pb "github.com/buoyantio/linkerd-buoyant/gen/bcloud"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	controlPlaneComponentLabel   = "linkerd.io/control-plane-component"
	identityComponentName        = "identity"
	linkerdProxyContainerName    = "linkerd-proxy"
	linkerdRootsEnvVarName       = "LINKERD2_PROXY_IDENTITY_TRUST_ANCHORS"
	proxyAdminPortName           = "linkerd-admin"
	linkerdNsEnvVarName          = "_l5d_ns"
	linkerdTrustDomainEnvVarName = "_l5d_trustdomain"
)

func (c *Client) GetControlPlaneCerts() (*pb.ControlPlaneCerts, error) {
	identityPod, err := c.getControlPlaneComponentPod(identityComponentName)
	if err != nil {
		return nil, err
	}

	container, err := getProxyContainer(identityPod)
	if err != nil {
		return nil, err
	}

	rootCerts, err := extractRootsCerts(container)
	if err != nil {
		return nil, err
	}

	issuerCerts, err := extractIssuerCertChain(identityPod, container)
	if err != nil {
		return nil, err
	}

	cpCerts := &pb.ControlPlaneCerts{
		IssuerCrtChain: issuerCerts,
		Roots:          rootCerts,
	}

	return cpCerts, nil
}

func (c *Client) getControlPlaneComponentPod(component string) (*v1.Pod, error) {
	selector := labels.Set(map[string]string{
		controlPlaneComponentLabel: component,
	}).AsSelector()

	pods, err := c.podLister.List(selector)
	if err != nil {
		c.log.Errorf("error listing pod: %s", err)
		return nil, err
	}

	if len(pods) == 0 {
		return nil, fmt.Errorf("could not find linkerd-%s pod", component)
	}

	for _, p := range pods {
		if p.Status.Phase == v1.PodRunning {
			return p, nil
		}
	}

	return nil, fmt.Errorf("could not find running pod for linkerd-%s", component)
}

func getProxyContainer(pod *v1.Pod) (*v1.Container, error) {
	for _, c := range pod.Spec.Containers {
		if c.Name == linkerdProxyContainerName {
			container := c
			return &container, nil
		}
	}

	return nil, fmt.Errorf("could not find proxy container in pod %s/%s", pod.Namespace, pod.Name)
}

func getProxyAdminPort(container *v1.Container) (int32, error) {
	for _, p := range container.Ports {
		if p.Name == proxyAdminPortName {
			return p.ContainerPort, nil
		}
	}

	return 0, fmt.Errorf("could not find port %s on proxy container [%s]", proxyAdminPortName, container.Name)
}

func getServerName(podsa string, podns string, container *v1.Container) (string, error) {
	var l5dns string
	var l5dtrustdomain string
	for _, env := range container.Env {
		if env.Name == linkerdNsEnvVarName {
			l5dns = env.Value
		}
		if env.Name == linkerdTrustDomainEnvVarName {
			l5dtrustdomain = env.Value
		}
	}

	if l5dns == "" {
		return "", fmt.Errorf("could not find %s env var on proxy container [%s]", linkerdNsEnvVarName, container.Name)
	}

	if l5dtrustdomain == "" {
		return "", fmt.Errorf("could not find %s env var on proxy container [%s]", linkerdTrustDomainEnvVarName, container.Name)
	}
	return fmt.Sprintf("%s.%s.serviceaccount.identity.%s.%s", podsa, podns, l5dns, l5dtrustdomain), nil
}

func extractRootsCerts(container *v1.Container) (*pb.CertData, error) {
	var roots []byte
	for _, ev := range container.Env {
		if ev.Name == linkerdRootsEnvVarName {
			roots = []byte(ev.Value)
		}
	}

	if roots == nil {
		return nil, fmt.Errorf("could not find env var with name %s on proxy container [%s]", linkerdRootsEnvVarName, container.Name)
	}

	return &pb.CertData{
		Raw: roots,
	}, nil
}

func extractIssuerCertChain(pod *v1.Pod, container *v1.Container) (*pb.CertData, error) {
	port, err := getProxyAdminPort(container)
	if err != nil {
		return nil, err
	}

	sn, err := getServerName(pod.Spec.ServiceAccountName, pod.ObjectMeta.Namespace, container)
	if err != nil {
		return nil, err
	}

	dialer := new(net.Dialer)
	dialer.Timeout = 5 * time.Second

	conn, err := tls.DialWithDialer(
		dialer,
		"tcp",
		fmt.Sprintf("%s:%d", pod.Status.PodIP, port), &tls.Config{
			InsecureSkipVerify: true,
			ServerName:         sn,
		})
	if err != nil {
		return nil, err
	}

	// skip the end cert
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) < 2 {
		return nil, fmt.Errorf("expected to get at least 2 peer certs, got %d", len(certs))
	}

	encodedCerts, err := encodeCertificatesPEM(certs[1:]...)
	if err != nil {
		return nil, err
	}
	certsData := []byte(encodedCerts)

	return &pb.CertData{
		Raw: certsData,
	}, nil
}

func encodeCertificatesPEM(crts ...*x509.Certificate) (string, error) {
	buf := bytes.Buffer{}
	for _, c := range crts {
		if err := encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}); err != nil {
			return "", err
		}
	}
	return buf.String(), nil
}

func encode(buf io.Writer, blk *pem.Block) error {
	if err := pem.Encode(buf, blk); err != nil {
		return err
	}
	return nil
}
