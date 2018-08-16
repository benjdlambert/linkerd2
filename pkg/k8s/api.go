package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"time"

	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/rest"

	// Load all the auth plugins for the cloud providers.
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

var minApiVersion = [3]int{1, 8, 0}

type KubernetesApi interface {
	UrlFor(namespace string, extraPathStartingWithSlash string) (*url.URL, error)
	NewClient() (*http.Client, error)
	GetVersionInfo(*http.Client) (*version.Info, error)
	CheckVersion(*version.Info) error
	CheckNamespaceExists(*http.Client, string) error
}

type kubernetesApi struct {
	*rest.Config
}

func (kubeapi *kubernetesApi) NewClient() (*http.Client, error) {
	secureTransport, err := rest.TransportFor(kubeapi.Config)
	if err != nil {
		return nil, fmt.Errorf("error instantiating Kubernetes API client: %v", err)
	}

	return &http.Client{
		Transport: secureTransport,
	}, nil
}

func (kubeapi *kubernetesApi) GetVersionInfo(client *http.Client) (*version.Info, error) {
	endpoint, err := url.Parse(kubeapi.Host + "/version")
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", endpoint.String(), nil)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rsp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer rsp.Body.Close()

	if rsp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Unexpected Kubernetes API response: %s", rsp.Status)
	}

	bytes, err := ioutil.ReadAll(rsp.Body)
	if err != nil {
		return nil, err
	}

	var versionInfo version.Info
	err = json.Unmarshal(bytes, &versionInfo)
	return &versionInfo, err
}

func (kubeapi *kubernetesApi) CheckVersion(versionInfo *version.Info) error {
	apiVersion, err := getK8sVersion(versionInfo.String())
	if err != nil {
		return err
	}

	if !isCompatibleVersion(minApiVersion, apiVersion) {
		return fmt.Errorf("Kubernetes is on version [%d.%d.%d], but version [%d.%d.%d] or more recent is required",
			apiVersion[0], apiVersion[1], apiVersion[2],
			minApiVersion[0], minApiVersion[1], minApiVersion[2])
	}

	return nil
}

func (kubeapi *kubernetesApi) CheckNamespaceExists(client *http.Client, namespace string) error {
	endpoint, err := url.Parse(kubeapi.Host + "/api/v1/namespaces/" + namespace)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("GET", endpoint.String(), nil)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rsp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		return err
	}
	defer rsp.Body.Close()

	if rsp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("The \"%s\" namespace does not exist", namespace)
	}

	if rsp.StatusCode != http.StatusOK {
		return fmt.Errorf("Unexpected Kubernetes API response: %s", rsp.Status)
	}

	return nil
}

// UrlFor generates a URL based on the Kubernetes config.
func (kubeapi *kubernetesApi) UrlFor(namespace string, extraPathStartingWithSlash string) (*url.URL, error) {
	return generateKubernetesApiBaseUrlFor(kubeapi.Host, namespace, extraPathStartingWithSlash)
}

// NewAPI returns a new KubernetesApi interface
func NewAPI(configPath string) (KubernetesApi, error) {
	config, err := getConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("error configuring Kubernetes API client: %v", err)
	}

	return &kubernetesApi{Config: config}, nil
}
