package main

import (
	"context"
	jsonencoding "encoding/json"
	"fmt"
	"reflect"
	"testing"

	v12 "github.com/openshift/api/apps/v1"
	fake2 "github.com/openshift/client-go/apps/clientset/versioned/fake"

	"github.com/skupperproject/skupper/api/types"
	"github.com/skupperproject/skupper/client"
	"github.com/skupperproject/skupper/pkg/event"
	"github.com/skupperproject/skupper/pkg/kube"
	"gotest.tools/assert"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestGetServiceDefinitionFromAnnotatedDeployment(t *testing.T) {

	event.StartDefaultEventStore(nil)

	// Types to compose test table
	type result struct {
		service types.ServiceInterface
		success bool
	}

	type test struct {
		name       string
		deployment *v1.Deployment
		expected   result
	}

	// Mock VanClient
	const NS = "test"
	vanClient := &client.VanClient{
		Namespace:  NS,
		KubeClient: fake.NewSimpleClientset(),
	}

	dm := &DefinitionMonitor{
		vanClient: vanClient,
		policy:    client.NewClusterPolicyValidator(vanClient),
	}

	// Help preparing sample deployments to compose test table
	newDeployment := func(name string, proxyAnnotationProtocol string, containerPortAnnotation string, addressAnnotation string, containerPort int, labels string, annotationString string, selectors map[string]string, tlsCert string, tlsTrust string) *v1.Deployment {
		// Add port to container if > 0
		containerPorts := []corev1.ContainerPort{}
		if containerPort > 0 {
			containerPorts = append(containerPorts, corev1.ContainerPort{
				Name:          "port",
				ContainerPort: int32(containerPort),
			})
		}

		// Prepare the container
		depContainers := []corev1.Container{{
			Name:  "container",
			Ports: containerPorts,
		}}

		// Deployment annotations
		annotations := map[string]string{}
		if proxyAnnotationProtocol != "" {
			annotations[types.ProxyQualifier] = proxyAnnotationProtocol
		}
		if containerPortAnnotation != "" {
			annotations[types.PortQualifier] = containerPortAnnotation
		}
		if addressAnnotation != "" {
			annotations[types.AddressQualifier] = addressAnnotation
		}
		if labels != "" {
			annotations[types.ServiceLabels] = labels
		}
		if annotationString != "" {
			annotations[types.ServiceAnnotations] = annotationString
		}

		if tlsCert != "" {
			annotations[types.TlsCertQualifier] = tlsCert
		}

		if tlsTrust != "" {
			annotations[types.TlsTrustQualifier] = tlsTrust
		}

		// Only initialize the selector pointer if a label has been provided
		var selector *metav1.LabelSelector
		if len(selectors) > 0 {
			selector = &metav1.LabelSelector{
				MatchLabels: selectors,
			}
		}
		return &v1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   NS,
				Annotations: annotations,
			},
			Spec: v1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: depContainers,
					},
				},
				Selector: selector,
			},
		}

	}

	// labels to use while preparing the test table
	labels := "app=app1"
	selectorWithLabels := map[string]string{"label1": "value1"}
	selectorWithoutLabels := map[string]string{}

	// test table below is meant to cover getServiceDefinitionFromAnnotatedDeployment()
	testTable := []test{
		{"no-proxy-annotation", newDeployment("dep1", "", "", "", 8080, labels, "", selectorWithLabels, "", ""), result{
			service: types.ServiceInterface{},
			success: false,
		}},
		{"http-port-annotation-no-address", newDeployment("dep1", "http", "81", "", 8080, labels, "", selectorWithLabels, "", ""), result{
			service: types.ServiceInterface{
				Address:  "dep1",
				Protocol: "http",
				Ports:    []int{81},
				Targets:  []types.ServiceInterfaceTarget{{Name: "dep1", Selector: "label1=value1"}},
				Labels:   map[string]string{"app": "app1"},
				Origin:   "annotation",
			},
			success: true,
		}},
		{"http-port-annotation-no-addess-without-selector", newDeployment("dep1", "http", "81", "", 8080, "", "", selectorWithoutLabels, "", ""), result{
			service: types.ServiceInterface{
				Address:  "dep1",
				Protocol: "http",
				Ports:    []int{81},
				Targets:  []types.ServiceInterfaceTarget{{Name: "dep1", Selector: ""}},
				Origin:   "annotation",
			},
			success: true,
		}},
		{"http-port-container-no-address", newDeployment("dep1", "http", "", "", 8080, "", "foo=bar", selectorWithLabels, "", ""), result{
			service: types.ServiceInterface{
				Address:     "dep1",
				Protocol:    "http",
				Ports:       []int{8080},
				Targets:     []types.ServiceInterfaceTarget{{Name: "dep1", Selector: "label1=value1"}},
				Annotations: map[string]string{"foo": "bar"},
				Origin:      "annotation",
			},
			success: true,
		}},
		{"http-no-port-no-address", newDeployment("dep1", "http", "", "", 0, "", "", selectorWithLabels, "", ""), result{
			service: types.ServiceInterface{
				Address:  "dep1",
				Protocol: "http",
				Ports:    []int{80},
				Targets:  []types.ServiceInterfaceTarget{{Name: "dep1", Selector: "label1=value1"}},
				Origin:   "annotation",
			},
			success: true,
		}},
		{"http-no-port-with-address", newDeployment("dep1", "http", "", "address1", 0, labels, "", selectorWithLabels, "", ""), result{
			service: types.ServiceInterface{
				Address:  "address1",
				Protocol: "http",
				Ports:    []int{80},
				Targets:  []types.ServiceInterfaceTarget{{Name: "dep1", Selector: "label1=value1"}},
				Labels:   map[string]string{"app": "app1"},
				Origin:   "annotation",
			},
			success: true,
		}},
		{"tcp-invalid-port-no-address", newDeployment("dep1", "tcp", "invalid", "", 0, "", "", selectorWithLabels, "", ""), result{
			service: types.ServiceInterface{},
			success: false,
		}},
		{"http-port-annotation-no-address-tls-enabled", newDeployment("dep1", "http2", "81", "", 8080, labels, "", selectorWithLabels, "skupper-tls-dep1", types.ServiceClientSecret), result{
			service: types.ServiceInterface{
				Address:          "dep1",
				Protocol:         "http2",
				Ports:            []int{81},
				Targets:          []types.ServiceInterfaceTarget{{Name: "dep1", Selector: "label1=value1"}},
				Labels:           map[string]string{"app": "app1"},
				Origin:           "annotation",
				TlsCredentials:   "skupper-tls-dep1",
				TlsCertAuthority: types.ServiceClientSecret,
			},
			success: true,
		}},
		{"http-port-annotation-no-address-tls-enabled-wrong-protocol", newDeployment("dep1", "http", "81", "", 8080, labels, "", selectorWithLabels, "skupper-tls-dep1", types.ServiceClientSecret), result{
			service: types.ServiceInterface{
				Protocol: "http",
				Ports:    []int{81},
			},
			success: false,
		}},
	}

	// Iterating through the test table
	for _, test := range testTable {
		t.Run(test.name, func(t *testing.T) {
			service, success := dm.getServiceDefinitionFromAnnotatedDeployment(test.deployment)
			// Validating returned service
			assert.Assert(t, reflect.DeepEqual(test.expected.service.Ports, service.Ports))
			assert.Equal(t, test.expected.service.Protocol, service.Protocol)
			assert.Equal(t, test.expected.service.Address, service.Address)
			assert.Equal(t, len(test.expected.service.Targets), len(service.Targets))
			if len(test.expected.service.Targets) > 0 {
				assert.Equal(t, test.expected.service.Targets[0].Name, service.Targets[0].Name)
				assert.Equal(t, test.expected.service.Targets[0].Selector, service.Targets[0].Selector)
			}
			assert.DeepEqual(t, test.expected.service.Labels, service.Labels)
			assert.DeepEqual(t, test.expected.service.Annotations, service.Annotations)
			assert.Equal(t, test.expected.service.Origin, service.Origin)
			assert.Equal(t, test.expected.service.TlsCredentials, service.TlsCredentials)
			assert.Equal(t, test.expected.service.TlsCertAuthority, service.TlsCertAuthority)
			// Validating overall result
			assert.Equal(t, success, test.expected.success)
		})
	}

}

func TestGetServiceDefinitionFromAnnotatedDeploymentConfig(t *testing.T) {

	event.StartDefaultEventStore(nil)

	// Types to compose test table
	type result struct {
		service types.ServiceInterface
		success bool
	}

	type test struct {
		name       string
		deployment *v12.DeploymentConfig
		expected   result
	}

	// Mock VanClient
	const NS = "test"
	vanClient := &client.VanClient{
		Namespace:    NS,
		KubeClient:   fake.NewSimpleClientset(),
		OCAppsClient: fake2.NewSimpleClientset(),
	}

	dm := &DefinitionMonitor{
		vanClient: vanClient,
		policy:    client.NewClusterPolicyValidator(vanClient),
	}

	// Help preparing sample deployments to compose test table
	newDeploymentConfig := func(name string, proxyAnnotationProtocol string, containerPortAnnotation string, addressAnnotation string, containerPort int, labels string, selectors map[string]string) *v12.DeploymentConfig {
		// Add port to container if > 0
		containerPorts := []corev1.ContainerPort{}
		if containerPort > 0 {
			containerPorts = append(containerPorts, corev1.ContainerPort{
				Name:          "port",
				ContainerPort: int32(containerPort),
			})
		}

		// Prepare the container
		depContainers := []corev1.Container{{
			Name:  "container",
			Ports: containerPorts,
		}}

		// Deployment annotations
		annotations := map[string]string{}
		if proxyAnnotationProtocol != "" {
			annotations[types.ProxyQualifier] = proxyAnnotationProtocol
		}
		if containerPortAnnotation != "" {
			annotations[types.PortQualifier] = containerPortAnnotation
		}
		if addressAnnotation != "" {
			annotations[types.AddressQualifier] = addressAnnotation
		}
		if labels != "" {
			annotations[types.ServiceLabels] = labels
		}

		// Only initialize the selector pointer if a label has been provided

		return &v12.DeploymentConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   NS,
				Annotations: annotations,
			},
			Spec: v12.DeploymentConfigSpec{
				Template: &corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: depContainers,
					},
				},
				Selector: selectors,
			},
		}

	}

	// labels to use while preparing the test table
	labels := "app=app1"
	selectorWithLabels := map[string]string{"label1": "value1"}
	selectorWithoutLabels := map[string]string{}

	// test table below is meant to cover getServiceDefinitionFromAnnotatedDeployment()
	testTable := []test{
		{"no-proxy-annotation", newDeploymentConfig("dep1", "", "", "", 8080, labels, selectorWithLabels), result{
			service: types.ServiceInterface{},
			success: false,
		}},
		{"http-port-annotation-no-address", newDeploymentConfig("dep1", "http", "81", "", 8080, labels, selectorWithLabels), result{
			service: types.ServiceInterface{
				Address:  "dep1",
				Protocol: "http",
				Ports:    []int{81},
				Targets:  []types.ServiceInterfaceTarget{{Name: "dep1", Selector: "label1=value1"}},
				Labels:   map[string]string{"app": "app1"},
				Origin:   "annotation",
			},
			success: true,
		}},
		{"http-port-annotation-no-addess-without-selector", newDeploymentConfig("dep1", "http", "81", "", 8080, "", selectorWithoutLabels), result{
			service: types.ServiceInterface{
				Address:  "dep1",
				Protocol: "http",
				Ports:    []int{81},
				Targets:  []types.ServiceInterfaceTarget{{Name: "dep1", Selector: ""}},
				Origin:   "annotation",
			},
			success: true,
		}},
		{"http-port-container-no-address", newDeploymentConfig("dep1", "http", "", "", 8080, "", selectorWithLabels), result{
			service: types.ServiceInterface{
				Address:  "dep1",
				Protocol: "http",
				Ports:    []int{8080},
				Targets:  []types.ServiceInterfaceTarget{{Name: "dep1", Selector: "label1=value1"}},
				Origin:   "annotation",
			},
			success: true,
		}},
		{"http-no-port-no-address", newDeploymentConfig("dep1", "http", "", "", 0, "", selectorWithLabels), result{
			service: types.ServiceInterface{
				Address:  "dep1",
				Protocol: "http",
				Ports:    []int{80},
				Targets:  []types.ServiceInterfaceTarget{{Name: "dep1", Selector: "label1=value1"}},
				Origin:   "annotation",
			},
			success: true,
		}},
		{"http-no-port-with-address", newDeploymentConfig("dep1", "http", "", "address1", 0, labels, selectorWithLabels), result{
			service: types.ServiceInterface{
				Address:  "address1",
				Protocol: "http",
				Ports:    []int{80},
				Targets:  []types.ServiceInterfaceTarget{{Name: "dep1", Selector: "label1=value1"}},
				Labels:   map[string]string{"app": "app1"},
				Origin:   "annotation",
			},
			success: true,
		}},
		{"tcp-invalid-port-no-address", newDeploymentConfig("dep1", "tcp", "invalid", "", 0, "", selectorWithLabels), result{
			service: types.ServiceInterface{},
			success: false,
		}},
	}

	// Iterating through the test table
	for _, test := range testTable {
		t.Run(test.name, func(t *testing.T) {
			service, success := dm.getServiceDefinitionFromAnnotatedDeploymentConfig(test.deployment)
			// Validating returned service
			assert.Assert(t, reflect.DeepEqual(test.expected.service.Ports, service.Ports))
			assert.Equal(t, test.expected.service.Protocol, service.Protocol)
			assert.Equal(t, test.expected.service.Address, service.Address)
			assert.Equal(t, len(test.expected.service.Targets), len(service.Targets))
			if len(test.expected.service.Targets) > 0 {
				assert.Equal(t, test.expected.service.Targets[0].Name, service.Targets[0].Name)
				assert.Equal(t, test.expected.service.Targets[0].Selector, service.Targets[0].Selector)
			}
			assert.DeepEqual(t, test.expected.service.Labels, service.Labels)
			assert.Equal(t, test.expected.service.Origin, service.Origin)
			// Validating overall result
			assert.Equal(t, success, test.expected.success)
		})
	}

}

func TestGetServiceDefinitionFromAnnotatedService(t *testing.T) {
	event.StartDefaultEventStore(nil)

	// Types to compose test table
	type result struct {
		service types.ServiceInterface
		success bool
	}

	type test struct {
		name     string
		service  *corev1.Service
		expected result
	}

	// Mock VanClient
	const NS = "test"
	vanClient := &client.VanClient{
		Namespace:  NS,
		KubeClient: fake.NewSimpleClientset(),
	}

	dm := &DefinitionMonitor{
		vanClient: vanClient,
		policy:    client.NewClusterPolicyValidator(vanClient),
	}

	// Helper used to prepare test table
	annotatedService := func(name string, proxyAnnotationProtocol string, addressAnnotation string, targetAnnotation string, labels string, annotationString string, selectorMap map[string]string, tlsCert string, tlsTrust string, originalSelector string, originalTargetPort string, targetPorts []int, ports ...int) *corev1.Service {

		annotations := map[string]string{}
		if proxyAnnotationProtocol != "" {
			annotations[types.ProxyQualifier] = proxyAnnotationProtocol
		}
		if addressAnnotation != "" {
			annotations[types.AddressQualifier] = addressAnnotation
		}
		if targetAnnotation != "" {
			annotations[types.TargetServiceQualifier] = targetAnnotation
		}

		if originalSelector != "" {
			annotations[types.OriginalSelectorQualifier] = originalSelector
		}

		if originalTargetPort != "" {
			annotations[types.OriginalTargetPortQualifier] = originalTargetPort
		}

		if labels != "" {
			annotations[types.ServiceLabels] = labels
		}
		if annotationString != "" {
			annotations[types.ServiceAnnotations] = annotationString
		}

		if tlsCert != "" {
			annotations[types.TlsCertQualifier] = tlsCert
		}

		if tlsTrust != "" {
			annotations[types.TlsTrustQualifier] = tlsTrust
		}

		// Only initialize the selector pointer if a label has been provided
		var selectors map[string]string
		if len(selectorMap) > 0 {
			selectors = selectorMap
		}

		// Only set ports, if at least one provided
		var svcPorts []corev1.ServicePort
		if len(ports) > 0 {
			for i, port := range ports {
				svcPorts = append(svcPorts, corev1.ServicePort{
					Name:       fmt.Sprintf("port%d", i),
					Port:       int32(port),
					TargetPort: intstr.FromInt(targetPorts[i]),
				})
			}
		}

		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Annotations: annotations,
			},
			Spec: corev1.ServiceSpec{
				Ports:    svcPorts,
				Selector: selectors,
			},
		}

	}

	// Create fake target services
	var err error
	// good path with target service providing port
	_, err = vanClient.KubeClient.CoreV1().Services(NS).Create(context.TODO(), annotatedService("targetsvc", "", "", "", "app=app1", "", nil, "", "", "", "", []int{0}, 8888), metav1.CreateOptions{})
	assert.NilError(t, err)
	// this is used to test case when protocol is http but target service does not provide a port, so it uses 80
	_, err = vanClient.KubeClient.CoreV1().Services(NS).Create(context.TODO(), annotatedService("targetsvcnoport", "", "", "", "app=app2", "", nil, "", "", "", "", []int{0}), metav1.CreateOptions{})
	assert.NilError(t, err)

	// Mock error when trying to get info for badtargetsvc
	vanClient.KubeClient.(*fake.Clientset).Fake.PrependReactor("get", "services", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		name := action.(k8stesting.GetAction).GetName()
		if name == "badtargetsvc" {
			return true, nil, fmt.Errorf("fake error has occurred")
		}
		return false, nil, nil
	})

	testTable := []test{
		{"no-proxy", annotatedService("", "", "", "", "", "", nil, "", "", "", "", []int{0}), result{
			service: types.ServiceInterface{},
			success: false,
		}},
		{"no-target-no-selector", annotatedService("svc", "http", "", "", "", "", nil, "", "", "", "", []int{0}), result{
			service: types.ServiceInterface{
				Address:  "svc",
				Protocol: "http",
				Ports:    []int{},
			},
			success: false,
		}},
		{"http-8080-targetsvc-8888", annotatedService("svc", "http", "address", "targetsvc", "app=app1", "", nil, "", "", "", "", []int{8888}, 8080), result{
			service: types.ServiceInterface{
				Address:  "address",
				Protocol: "http",
				Ports:    []int{8080},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:        "svc",
						Selector:    "",
						TargetPorts: map[int]int{8080: 8888},
						Service:     "targetsvc",
					},
				},
				Labels: map[string]string{
					"app": "app1",
				},
				Origin: "annotation",
			},
			success: true,
		}},
		{"http-80-targetsvcnoport", annotatedService("svc", "http", "address", "targetsvcnoport", "app=app1", "", nil, "", "", "", "", []int{}), result{
			service: types.ServiceInterface{
				Address:  "address",
				Protocol: "http",
				Ports:    []int{80},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:     "svc",
						Selector: "",
						Service:  "targetsvcnoport",
					},
				},
				Labels: map[string]string{
					"app": "app1",
				},
				Origin: "annotation",
			},
			success: true,
		}},
		{"tcp-noport-targetsvcnoport", annotatedService("svc", "tcp", "address", "targetsvcnoport", "", "", nil, "", "", "", "", []int{0}), result{
			service: types.ServiceInterface{
				Address:  "address",
				Protocol: "tcp",
				Ports:    []int{},
			},
			success: false,
		}},
		{"tcp-noport-targetsvc-8888", annotatedService("svc", "tcp", "address", "targetsvc", "", "", nil, "", "", "", "", []int{}), result{
			service: types.ServiceInterface{
				Address:  "address",
				Protocol: "tcp",
				Ports:    []int{8888},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:        "svc",
						Selector:    "",
						TargetPorts: map[int]int{8888: 8888},
						Service:     "targetsvc",
					},
				},
				Origin: "annotation",
			},
			success: true,
		}},
		{"bad-target-service", annotatedService("svc", "http", "address", "badtargetsvc", "", "", nil, "", "", "", "", []int{0}, 8080), result{
			service: types.ServiceInterface{
				Address:  "address",
				Protocol: "http",
				Ports:    []int{8080},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:     "svc",
						Selector: "",
						Service:  "badtargetsvc",
					},
				},
				Origin: "annotation",
			},
			success: true,
		}},
		{"tcp-noport-targetsvc-8888", annotatedService("svc", "tcp", "address", "targetsvc", "", "", nil, "", "", "", "", []int{}), result{
			service: types.ServiceInterface{
				Address:  "address",
				Protocol: "tcp",
				Ports:    []int{8888},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:        "svc",
						Selector:    "",
						TargetPorts: map[int]int{8888: 8888},
						Service:     "targetsvc",
					},
				},
				Origin: "annotation",
			},
			success: true,
		}},
		{"tcp-noport-selector", annotatedService("svc", "tcp", "address", "",
			"", "", map[string]string{"label1": "value1"}, "", "", "", "", []int{0}), result{
			service: types.ServiceInterface{
				Address:  "address",
				Protocol: "tcp",
				Ports:    []int{},
			},
			success: false,
		}},
		{"http-noport-selector", annotatedService("svc", "http", "address", "",
			"", "foo=bar", map[string]string{"label1": "value1"}, "", "", "", "", []int{0}), result{
			service: types.ServiceInterface{
				Address:  "address",
				Protocol: "http",
				Ports:    []int{80},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:     "svc",
						Selector: "label1=value1",
					},
				},
				Annotations: map[string]string{
					"foo": "bar",
				},
				Origin: "annotation",
			},
			success: true,
		}},
		{"http-8080-selector", annotatedService("svc", "http", "address", "",
			"", "", map[string]string{"label1": "value1"}, "", "", "", "", []int{8888}, 8080), result{
			service: types.ServiceInterface{
				Address:  "address",
				Protocol: "http",
				Ports:    []int{8080},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:        "svc",
						Selector:    "label1=value1",
						TargetPorts: map[int]int{8080: 8888},
					},
				},
				Origin: "annotation",
			},
			success: true,
		}},
		{"http-8080-original-selector",
			annotatedService("svc", "http", "address", "",
				"", "", map[string]string{types.ComponentAnnotation: types.RouterComponent, types.ProxyQualifier: "http"},
				"", "", "label1=value1", "8080", []int{1024}, 8080),
			result{
				service: types.ServiceInterface{
					Address:  "address",
					Protocol: "http",
					Ports:    []int{8080},
					Targets: []types.ServiceInterfaceTarget{
						{
							Name:        "svc",
							Selector:    "label1=value1",
							TargetPorts: map[int]int{8080: 8080},
						},
					},
					Origin: "annotation",
				},
				success: true,
			},
		},
		{"http-8080-bad-original-selector", annotatedService("svc", "http", "", "", "", "", map[string]string{
			types.ComponentAnnotation: types.RouterComponent,
		}, "", "", "", "", []int{0}, 8080), result{
			service: types.ServiceInterface{
				Address:  "svc",
				Protocol: "http",
				Ports:    []int{8080},
				Targets:  []types.ServiceInterfaceTarget{},
			},
			success: false,
		}},
		{"http-8080-targetsvc-8888-tls-enabled", annotatedService("svc", "http2", "address", "targetsvc", "app=app1", "", nil, "skupper-tls-svc", types.TlsCertQualifier, "", "", []int{8888}, 8080), result{
			service: types.ServiceInterface{
				Address:  "address",
				Protocol: "http2",
				Ports:    []int{8080},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:        "svc",
						Selector:    "",
						TargetPorts: map[int]int{8080: 8888},
						Service:     "targetsvc",
					},
				},
				Labels: map[string]string{
					"app": "app1",
				},
				Origin:           "annotation",
				TlsCredentials:   "skupper-tls-svc",
				TlsCertAuthority: types.TlsCertQualifier,
			},
			success: true,
		}},
		{"http-8080-targetsvc-8888-tls-enabled-wrong-protocol", annotatedService("svc", "http", "address", "targetsvc", "app=app1", "", nil, "skupper-tls-address", types.TlsCertQualifier, "", "", []int{8888}, 8080), result{
			service: types.ServiceInterface{
				Protocol: "http",
				Ports:    []int{8080},
			},
			success: false,
		}},
	}

	for _, test := range testTable {
		t.Run(test.name, func(t *testing.T) {
			service, success := dm.getServiceDefinitionFromAnnotatedService(test.service)
			assert.Equal(t, test.expected.success, success)
			assert.Assert(t, reflect.DeepEqual(test.expected.service.Ports, service.Ports))
			assert.Equal(t, test.expected.service.Address, service.Address)
			assert.Equal(t, len(test.expected.service.Targets), len(service.Targets))
			if len(service.Targets) > 0 {
				assert.Equal(t, test.expected.service.Targets[0].Name, service.Targets[0].Name)
				assert.Equal(t, test.expected.service.Targets[0].Service, service.Targets[0].Service)
				assert.Assert(t, reflect.DeepEqual(test.expected.service.Targets[0].TargetPorts, service.Targets[0].TargetPorts))
				assert.Equal(t, test.expected.service.Targets[0].Selector, service.Targets[0].Selector)
			}
			assert.DeepEqual(t, test.expected.service.Labels, service.Labels)
			for key, value := range test.expected.service.Annotations {
				assert.Equal(t, value, service.Annotations[key])
			}
			assert.Equal(t, test.expected.service.Origin, service.Origin)
			assert.Equal(t, test.expected.service.TlsCertAuthority, service.TlsCertAuthority)
			assert.Equal(t, test.expected.service.TlsCredentials, service.TlsCredentials)
		})
	}

}

func TestDeducePort(t *testing.T) {

	// Helps generating the test table
	type test struct {
		name          string
		deployment    *v1.Deployment
		expectedPorts map[int]int
	}

	newDeployment := func(portAnnotation string, portContainer ...int) *v1.Deployment {
		annotationMap := map[string]string{}
		if portAnnotation != "" {
			annotationMap["skupper.io/port"] = portAnnotation
		}

		ports := []corev1.ContainerPort{}
		for _, p := range portContainer {
			ports = append(ports, corev1.ContainerPort{ContainerPort: int32(p)})
		}

		return &v1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "dep1",
				Namespace:   "ns1",
				Annotations: annotationMap,
			},
			Spec: v1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Ports: ports,
						}},
					},
				},
			},
		}
	}

	testTable := []test{
		{"no-annotation-container-port", newDeployment("", 8080), map[int]int{8080: 8080}},
		{"no-annotation-container-ports", newDeployment("", 8080, 9090), map[int]int{8080: 8080, 9090: 9090}},
		{"valid-annotation-container-port", newDeployment("8888", 8080), map[int]int{8888: 8888}},
		{"valid-annotation-container-diff-ports", newDeployment("8888:8080", 8080), map[int]int{8888: 8080}},
		{"invalid-annotation-container-port", newDeployment("invalid", 8080), map[int]int{}},
	}

	for _, test := range testTable {
		t.Run(test.name, func(t *testing.T) {
			assert.Assert(t, reflect.DeepEqual(test.expectedPorts, deducePort(test.deployment)))
		})
	}
}

func TestDeducePortFromService(t *testing.T) {
	type test struct {
		name     string
		service  *corev1.Service
		expected map[int]int
	}

	// Helper used to prepare test table
	newService := func(ports ...int) *corev1.Service {
		// Only set ports, if at least one provided
		var svcPorts []corev1.ServicePort
		if len(ports) > 0 {
			for i, port := range ports {
				svcPorts = append(svcPorts, corev1.ServicePort{
					Name: fmt.Sprintf("port%d", i),
					Port: int32(port),
				})
			}
		}
		return &corev1.Service{
			Spec: corev1.ServiceSpec{
				Ports: svcPorts,
			},
		}
	}

	testTable := []test{
		{"no-port", newService(), map[int]int{}},
		{"one-port", newService(8080), map[int]int{8080: 8080}},
		{"two-ports", newService(8080, 8081), map[int]int{8080: 8080, 8081: 8081}},
	}

	for _, test := range testTable {
		t.Run(test.name, func(t *testing.T) {
			deducedPorts := deducePortFromService(test.service)
			assert.Assert(t, reflect.DeepEqual(deducedPorts, test.expected), "got: %v - expected: %v", deducedPorts, test.expected)
		})
	}
}

func TestDeducePortFromServiceWithTargets(t *testing.T) {
	type test struct {
		name     string
		service  *corev1.Service
		expected map[int]int
	}

	// Helper used to prepare test table
	newService := func(ports ...[]int) *corev1.Service {
		// Only set ports, if at least one provided
		var svcPorts []corev1.ServicePort
		if len(ports) > 0 {
			for i, port := range ports {
				svcPorts = append(svcPorts, corev1.ServicePort{
					Name:       fmt.Sprintf("port%d", i),
					Port:       int32(port[0]),
					TargetPort: intstr.FromInt(port[1]),
				})
			}
		}
		return &corev1.Service{
			Spec: corev1.ServiceSpec{
				Ports: svcPorts,
			},
		}
	}

	testTable := []test{
		{"no-port", newService(), map[int]int{}},
		{"one-port", newService([]int{80, 8080}), map[int]int{80: 8080}},
		{"two-ports", newService([]int{80, 8080}, []int{81, 8081}), map[int]int{80: 8080, 81: 8081}},
	}

	for _, test := range testTable {
		t.Run(test.name, func(t *testing.T) {
			deducedPorts := deducePortFromService(test.service)
			assert.Assert(t, reflect.DeepEqual(deducedPorts, test.expected))
		})
	}
}

func TestUpdateAnnotatedServiceDefinition(t *testing.T) {
	type test struct {
		name    string
		actual  types.ServiceInterface
		desired types.ServiceInterface
		result  bool
		targets []types.ServiceInterfaceTarget
	}

	tests := []test{
		{
			name: "one",
			actual: types.ServiceInterface{
				Address:  "one",
				Protocol: "tcp",
				Origin:   "annotation",
				Ports:    []int{8080},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:     "t1",
						Selector: "foo=bar",
						TargetPorts: map[int]int{
							8080: 1024,
						},
					},
				},
			},
			desired: types.ServiceInterface{
				Address:  "one",
				Protocol: "tcp",
				Ports:    []int{8080},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:     "t2",
						Selector: "bar=baz",
						TargetPorts: map[int]int{
							8080: 1025,
						},
					},
				},
			},
			result: true,
			targets: []types.ServiceInterfaceTarget{
				{
					Name:     "t1",
					Selector: "foo=bar",
					TargetPorts: map[int]int{
						8080: 1024,
					},
				},
				{
					Name:     "t2",
					Selector: "bar=baz",
					TargetPorts: map[int]int{
						8080: 1025,
					},
				},
			},
		},
		{
			name: "two",
			actual: types.ServiceInterface{
				Address:  "two",
				Protocol: "tcp",
				Origin:   "annotation",
				Ports:    []int{8080},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:     "t1",
						Selector: "foo=bar",
						TargetPorts: map[int]int{
							8080: 1024,
						},
					},
				},
			},
			desired: types.ServiceInterface{
				Address:  "two",
				Protocol: "tcp",
				Ports:    []int{8080},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:     "t1",
						Selector: "bar=baz",
						TargetPorts: map[int]int{
							8080: 1025,
						},
					},
				},
			},
			result: true,
			targets: []types.ServiceInterfaceTarget{
				{
					Name:     "t1",
					Selector: "bar=baz",
					TargetPorts: map[int]int{
						8080: 1025,
					},
				},
			},
		},
		{
			name: "three",
			actual: types.ServiceInterface{
				Address:  "three",
				Protocol: "tcp",
				Origin:   "annotation",
				Ports:    []int{8080},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:     "t1",
						Selector: "foo=bar",
						TargetPorts: map[int]int{
							8080: 1024,
						},
					},
				},
			},
			desired: types.ServiceInterface{
				Address:  "three",
				Protocol: "tcp",
				Ports:    []int{8080},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:     "t1",
						Selector: "foo=bar",
						TargetPorts: map[int]int{
							8080: 1024,
						},
					},
				},
			},
			result: false,
			targets: []types.ServiceInterfaceTarget{
				{
					Name:     "t1",
					Selector: "foo=bar",
					TargetPorts: map[int]int{
						8080: 1024,
					},
				},
			},
		},
		{
			name: "four",
			actual: types.ServiceInterface{
				Address:  "four",
				Protocol: "tcp",
				Origin:   "annotation",
				Ports:    []int{8080},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:     "t1",
						Selector: "foo=bar",
						TargetPorts: map[int]int{
							8080: 1024,
						},
					},
				},
			},
			desired: types.ServiceInterface{
				Address:  "four",
				Protocol: "tcp",
				Ports:    []int{8080},
				Labels: map[string]string{
					"foo": "bar",
				},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:     "t1",
						Selector: "foo=bar",
						TargetPorts: map[int]int{
							8080: 1024,
						},
					},
				},
			},
			result: true,
			targets: []types.ServiceInterfaceTarget{
				{
					Name:     "t1",
					Selector: "foo=bar",
					TargetPorts: map[int]int{
						8080: 1024,
					},
				},
			},
		},
		{
			name: "five",
			actual: types.ServiceInterface{
				Address:  "five",
				Protocol: "tcp",
				Origin:   "annotation",
				Ports:    []int{8080},
				Annotations: map[string]string{
					"foo": "bar",
				},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:     "t1",
						Selector: "foo=bar",
						TargetPorts: map[int]int{
							8080: 1024,
						},
					},
				},
			},
			desired: types.ServiceInterface{
				Address:  "five",
				Protocol: "tcp",
				Ports:    []int{8080},
				Annotations: map[string]string{
					"foo": "baz",
				},
				Targets: []types.ServiceInterfaceTarget{
					{
						Name:     "t1",
						Selector: "foo=bar",
						TargetPorts: map[int]int{
							8080: 1024,
						},
					},
				},
			},
			result: true,
			targets: []types.ServiceInterfaceTarget{
				{
					Name:     "t1",
					Selector: "foo=bar",
					TargetPorts: map[int]int{
						8080: 1024,
					},
				},
			},
		},
	}
	event.StartDefaultEventStore(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := updateAnnotatedServiceDefinition(&test.actual, &test.desired)
			assert.Equal(t, result, test.result)
			assert.Assert(t, reflect.DeepEqual(indexTargetsByName(test.desired.Targets), indexTargetsByName(test.targets)), "Expected %v, got %v", test.targets, test.desired.Targets)
		})
	}
}

func indexTargetsByName(targets []types.ServiceInterfaceTarget) map[string]types.ServiceInterfaceTarget {
	results := map[string]types.ServiceInterfaceTarget{}
	for _, target := range targets {
		results[target.Name] = target
	}
	return results
}

func encodeServiceDefinitions(services []types.ServiceInterface) (map[string]string, error) {
	results := map[string]string{}
	for _, service := range services {
		encoded, err := jsonencoding.Marshal(service)
		if err != nil {
			return nil, err
		}
		results[service.Address] = string(encoded)
	}
	return results, nil
}

func decodeServiceDefinitions(data map[string]string) (map[string]types.ServiceInterface, error) {
	services := map[string]types.ServiceInterface{}
	for _, encoded := range data {
		service := types.ServiceInterface{}
		err := jsonencoding.Unmarshal([]byte(encoded), &service)
		if err != nil {
			return nil, err
		}
		services[service.Address] = service
	}
	return services, nil
}

func indexByAddress(services []types.ServiceInterface) map[string]types.ServiceInterface {
	results := map[string]types.ServiceInterface{}
	for _, service := range services {
		results[service.Address] = service
	}
	return results
}

func TestDeleteServiceDefinitionForAddress(t *testing.T) {
	type test struct {
		name     string
		initial  []types.ServiceInterface
		address  string
		target   string
		expected []types.ServiceInterface
	}

	tests := []test{
		{
			name: "one",
			initial: []types.ServiceInterface{
				{
					Address:  "foo",
					Protocol: "tcp",
					Ports:    []int{8080},
					Targets: []types.ServiceInterfaceTarget{
						{
							Name:     "bar",
							Selector: "x=y",
							TargetPorts: map[int]int{
								8080: 1024,
							},
						},
					},
				},
				{
					Address:  "other",
					Protocol: "tcp",
					Ports:    []int{8080},
					Targets: []types.ServiceInterfaceTarget{
						{
							Name:     "bar",
							Selector: "x=y",
							TargetPorts: map[int]int{
								8080: 1024,
							},
						},
					},
				},
			},
			address: "foo",
			target:  "bar",
			expected: []types.ServiceInterface{
				{
					Address:  "other",
					Protocol: "tcp",
					Ports:    []int{8080},
					Targets: []types.ServiceInterfaceTarget{
						{
							Name:     "bar",
							Selector: "x=y",
							TargetPorts: map[int]int{
								8080: 1024,
							},
						},
					},
				},
			},
		},
		{
			name: "two",
			initial: []types.ServiceInterface{
				{
					Address:  "foo",
					Protocol: "tcp",
					Ports:    []int{8080},
					Targets: []types.ServiceInterfaceTarget{
						{
							Name:     "bar",
							Selector: "x=y",
							TargetPorts: map[int]int{
								8080: 1024,
							},
						},
						{
							Name:     "baz",
							Selector: "a=b",
							TargetPorts: map[int]int{
								8080: 1025,
							},
						},
					},
				},
				{
					Address:  "other",
					Protocol: "tcp",
					Ports:    []int{8080},
					Targets: []types.ServiceInterfaceTarget{
						{
							Name:     "bar",
							Selector: "x=y",
							TargetPorts: map[int]int{
								8080: 1024,
							},
						},
					},
				},
			},
			address: "foo",
			target:  "bar",
			expected: []types.ServiceInterface{
				{
					Address:  "foo",
					Protocol: "tcp",
					Ports:    []int{8080},
					Targets: []types.ServiceInterfaceTarget{
						{
							Name:     "baz",
							Selector: "a=b",
							TargetPorts: map[int]int{
								8080: 1025,
							},
						},
					},
				},
				{
					Address:  "other",
					Protocol: "tcp",
					Ports:    []int{8080},
					Targets: []types.ServiceInterfaceTarget{
						{
							Name:     "bar",
							Selector: "x=y",
							TargetPorts: map[int]int{
								8080: 1024,
							},
						},
					},
				},
			},
		},
	}
	event.StartDefaultEventStore(nil)
	const NS = "test"
	vanClient := &client.VanClient{
		Namespace:  NS,
		KubeClient: fake.NewSimpleClientset(),
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dm := &DefinitionMonitor{
				vanClient: vanClient,
				policy:    client.NewClusterPolicyValidator(vanClient),
			}
			dm.annotated = indexByAddress(test.initial)
			data, err := encodeServiceDefinitions(test.initial)
			assert.NilError(t, err)
			_, err = kube.NewConfigMap("skupper-services", &data, nil, nil, nil, vanClient.Namespace, vanClient.KubeClient)
			assert.NilError(t, err)
			err = dm.deleteServiceDefinitionForAddress(test.address, test.target)
			assert.NilError(t, err)
			latest, err := kube.GetConfigMap("skupper-services", vanClient.Namespace, vanClient.KubeClient)
			assert.NilError(t, err)
			actual, err := decodeServiceDefinitions(latest.Data)
			assert.NilError(t, err)
			expected := indexByAddress(test.expected)
			assert.Assert(t, reflect.DeepEqual(actual, expected), "Expected %v, got %v", expected, actual)

		})
	}

}
