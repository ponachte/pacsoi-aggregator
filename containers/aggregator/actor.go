package main

import (
	"aggregator/auth"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/context"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var serverMux *http.ServeMux

func InitializeKubernetes(mux *http.ServeMux) {
	serverMux = mux
}

type Actor struct {
	Id                  string `json:"id"`
	PipelineDescription string `json:"pipelineDescription"`
	pod                 *v1.Pod
}

// TODO This needs to be more generic and extensible
func createActor(pipelineDescription string) (Actor, error) {
	id := uuid.New().String()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	namespace := "aggregator-ns"

	// ----------------------------
	// 1. Pod spec
	// ----------------------------
	image := os.Getenv("TRANSFORMATION")
	if image == "" {
		return Actor{}, fmt.Errorf("TRANSFORMATION env var is not set")
	}

	podSpec := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: id,
			Labels: map[string]string{
				"app": id,
			},
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Name:            "transformation",
					Image:           image,
					ImagePullPolicy: v1.PullNever,
					Env: []v1.EnvVar{
						{Name: "PIPELINE_DESCRIPTION", Value: pipelineDescription},
						// {Name: "SSL_CERT_FILE", Value: "/key-pair/uma-proxy.crt"},
						{Name: "HTTP_PROXY", Value: "http://uma-proxy.uma-proxy-ns.svc.cluster.local:8080"},
						{Name: "HTTPS_PROXY", Value: "http://uma-proxy.uma-proxy-ns.svc.cluster.local:8443"},
						{Name: "http_proxy", Value: "http://uma-proxy.uma-proxy-ns.svc.cluster.local:8080"},
						{Name: "https_proxy", Value: "http://uma-proxy.uma-proxy-ns.svc.cluster.local:8443"},
					},
					Ports: []v1.ContainerPort{
						{ContainerPort: 8080},
					},
					VolumeMounts: []v1.VolumeMount{
						{
							Name:      "key-pair",
							MountPath: "/key-pair",
							ReadOnly:  true,
						},
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: "key-pair",
					VolumeSource: v1.VolumeSource{
						Secret: &v1.SecretVolumeSource{
							SecretName: "uma-proxy-key-pair",
						},
					},
				},
			},
			RestartPolicy: v1.RestartPolicyNever,
		},
	}

	// ----------------------------
	// 2. Create Pod
	// ----------------------------
	pod, err := Clientset.CoreV1().Pods(namespace).Create(ctx, podSpec, metav1.CreateOptions{})
	if err != nil {
		return Actor{}, fmt.Errorf("failed to create pod in namespace %s: %w", namespace, err)
	}

	// ----------------------------
	// 3. Service spec (NodePort for minikube)
	// ----------------------------
	serviceName := "id-" + id + "-service"
	serviceSpec := &v1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: serviceName,
		},
		Spec: v1.ServiceSpec{
			Type: v1.ServiceTypeClusterIP,
			Selector: map[string]string{
				"app": id,
			},
			Ports: []v1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt(8080),
				},
			},
		},
	}

	_, err = Clientset.CoreV1().Services(namespace).Create(ctx, serviceSpec, metav1.CreateOptions{})
	if err != nil {
		return Actor{}, fmt.Errorf("failed to create service in namespace %s: %w", namespace, err)
	}

	// ----------------------------
	// 4. Wait for Pod Running
	// ----------------------------
	watcher, err := Clientset.CoreV1().Pods(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", id),
	})
	if err != nil {
		return Actor{}, err
	}
	defer watcher.Stop()

podLoop:
	for event := range watcher.ResultChan() {
		p, ok := event.Object.(*v1.Pod)
		if !ok {
			continue
		}
		switch p.Status.Phase {
		case v1.PodRunning:
			break podLoop
		case v1.PodFailed:
			return Actor{}, fmt.Errorf("pod failed: %v", p.Status.Reason)
		}
	}

	// -----------------------------
	// 5. Register handler in aggregator
	// -----------------------------
	serviceURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:80", serviceName, namespace)
	serverMux.HandleFunc("/actors/"+id, func(w http.ResponseWriter, r *http.Request) {
		if !auth.AuthorizeRequest(w, r, nil) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		req, err := http.NewRequest(r.Method, serviceURL, r.Body)
		if err != nil {
			http.Error(w, "failed to create request", http.StatusInternalServerError)
			return
		}
		req.Header = r.Header.Clone()

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Println("Failed to reach actor service:", err.Error())
			http.Error(w, "failed to reach actor service", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	// -----------------------------
	// 6. Return actor object
	// -----------------------------
	return Actor{
		Id:                  id,
		PipelineDescription: pipelineDescription,
		pod:                 pod,
	}, nil
}

// Stop deletes the pod + service and runs cleanup (kills port-forward).
func (actor Actor) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if actor.pod != nil {
		// Delete pod
		err := Clientset.CoreV1().Pods("default").Delete(ctx, actor.pod.Name, metav1.DeleteOptions{})
		if err != nil {
			fmt.Println("Error deleting pod:", err.Error())
		} else {
			fmt.Println("Pod deleted successfully:", actor.pod.Name)
		}
	}

	// Delete service (service name is tied to actor.Id)
	serviceName := "id-" + actor.Id + "-service"
	err := Clientset.CoreV1().Services("default").Delete(ctx, serviceName, metav1.DeleteOptions{})
	if err != nil {
		fmt.Println("Error deleting service:", err.Error())
	} else {
		fmt.Println("Service deleted successfully:", serviceName)
	}
}

// TODO: should return the status of the actor (running, stopped, errors, ect.)
func (actor Actor) marshalActor() string {
	pipelineForJson := strings.ReplaceAll(actor.PipelineDescription, `"`, `\"`)
	pipelineForJson = strings.ReplaceAll(pipelineForJson, "\n", `\n`)
	actorJson := fmt.Sprintf(
		`{"id":"%s","transformation":"%s"}`,
		actor.Id,
		pipelineForJson,
	)
	return actorJson
}
