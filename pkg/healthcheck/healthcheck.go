package healthcheck

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/linkerd/linkerd2/controller/api/public"
	healthcheckPb "github.com/linkerd/linkerd2/controller/gen/common/healthcheck"
	pb "github.com/linkerd/linkerd2/controller/gen/public"
	"github.com/linkerd/linkerd2/pkg/k8s"
	"github.com/linkerd/linkerd2/pkg/version"
	k8sVersion "k8s.io/apimachinery/pkg/version"
)

type checker struct {
	category    string
	description string
	fatal       bool
	check       func() error
	checkRpc    func() (*healthcheckPb.SelfCheckResponse, error)
}

type CheckObserver func(string, string, error)

type HealthChecker struct {
	checkers      []*checker // TODO: category map?
	kubeApi       k8s.KubernetesApi
	httpClient    *http.Client
	kubeVersion   *k8sVersion.Info
	apiClient     pb.ApiClient
	latestVersion string
}

func NewHealthChecker() *HealthChecker {
	return &HealthChecker{
		checkers: make([]*checker, 0),
	}
}

func (hc *HealthChecker) AddKubernetesAPIChecks(kubeconfigPath, controlPlaneNamespace string) {
	hc.checkers = append(hc.checkers, &checker{
		category:    "kubernetes-api",
		description: "can initialize the client",
		fatal:       true,
		check: func() (err error) {
			hc.kubeApi, err = k8s.NewAPI(kubeconfigPath)
			return
		},
	})

	hc.checkers = append(hc.checkers, &checker{
		category:    "kubernetes-api",
		description: "can query the Kubernetes API",
		fatal:       true,
		check: func() (err error) {
			hc.httpClient, err = hc.kubeApi.NewClient()
			if err != nil {
				return
			}
			hc.kubeVersion, err = hc.kubeApi.GetVersionInfo(hc.httpClient)
			return
		},
	})

	hc.checkers = append(hc.checkers, &checker{
		category:    "kubernetes-api",
		description: "is running the minimum Kubernetes API version",
		fatal:       false,
		check: func() error {
			return hc.kubeApi.CheckVersion(hc.kubeVersion)
		},
	})

	hc.checkers = append(hc.checkers, &checker{
		category:    "kubernetes-api",
		description: "control plane namespace exists",
		fatal:       true,
		check: func() error {
			return hc.kubeApi.CheckNamespaceExists(hc.httpClient, controlPlaneNamespace)
		},
	})
}

func (hc *HealthChecker) AddLinkerdAPIChecks(apiAddr, controlPlaneNamespace string) {
	hc.checkers = append(hc.checkers, &checker{
		category:    "linkerd-api",
		description: "can initialize the client",
		fatal:       true,
		check: func() (err error) {
			if apiAddr != "" {
				hc.apiClient, err = public.NewInternalClient(controlPlaneNamespace, apiAddr)
			} else {
				hc.apiClient, err = public.NewExternalClient(controlPlaneNamespace, hc.kubeApi)
			}
			return
		},
	})

	hc.checkers = append(hc.checkers, &checker{
		category:    "linkerd-api",
		description: "can query the control plane API",
		fatal:       true,
		checkRpc: func() (*healthcheckPb.SelfCheckResponse, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return hc.apiClient.SelfCheck(ctx, &healthcheckPb.SelfCheckRequest{})
		},
	})
}

func (hc *HealthChecker) AddLinkerdVersionChecks(versionOverride string) {
	hc.checkers = append(hc.checkers, &checker{
		category:    "linkerd-version",
		description: "can get the latest version",
		fatal:       true,
		check: func() (err error) {
			if versionOverride != "" {
				hc.latestVersion = versionOverride
			} else {
				hc.latestVersion, err = version.GetLatestVersion()
			}
			return
		},
	})

	hc.checkers = append(hc.checkers, &checker{
		category:    "linkerd-version",
		description: "cli is up-to-date",
		fatal:       false,
		check: func() error {
			return version.CheckClientVersion(hc.latestVersion)
		},
	})

	hc.checkers = append(hc.checkers, &checker{
		category:    "linkerd-version",
		description: "control plane is up-to-date",
		fatal:       false,
		check: func() error {
			return version.CheckServerVersion(hc.apiClient, hc.latestVersion)
		},
	})
}

func (hc *HealthChecker) RunChecks(observe CheckObserver) bool {
	success := true

	for _, checker := range hc.checkers {
		if checker.check != nil {
			err := checker.check()
			observe(checker.category, checker.description, err)
			if err != nil {
				success = false
				if checker.fatal {
					break
				}
			}
		}

		if checker.checkRpc != nil {
			checkRsp, err := checker.checkRpc()
			observe(checker.category, checker.description, err)
			if err != nil {
				success = false
				if checker.fatal {
					break
				}
				continue
			}

			for _, check := range checkRsp.Results {
				category := fmt.Sprintf("%s[%s]", checker.category, check.SubsystemName)
				var err error
				if check.Status != healthcheckPb.CheckStatus_OK {
					success = false
					err = fmt.Errorf(check.FriendlyMessageToUser)
				}
				observe(category, check.CheckDescription, err)
			}
		}
	}

	return success
}

func (hc *HealthChecker) PublicAPIClient() pb.ApiClient {
	return hc.apiClient
}
