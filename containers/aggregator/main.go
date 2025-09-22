package main

import (
	"aggregator/auth"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/net/context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// var protocol = "http"
var internalHost string
var internalPort string
var externalHost string
var externalPort string

var Clientset *kubernetes.Clientset

func main() {
	// ------------------------
	// Read host and port from environment variables
	// ------------------------
	internalHost = os.Getenv("AGGREGATOR_HOST")
	internalPort = os.Getenv("AGGREGATOR_PORT")

	if internalHost == "" || internalPort == "" {
		log.Fatal("Environment variables AGGREGATOR_HOST and AGGREGATOR_PORT must be set")
	}

	externalHost = os.Getenv("AGGREGATOR_EXTERNAL_HOST")
	externalPort = os.Getenv("AGGREGATOR_EXTERNAL_PORT")

	if externalHost == "" || externalPort == "" {
		log.Fatal("Environment variables AGGREGATOR_EXTERNAL_HOST and AGGREGATOR_EXTERNAL_PORT must be set")
	}

	// ------------------------
	// Load in-cluster config
	// ------------------------
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Failed to load in-cluster config: %v", err)
	}

	Clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// ------------------------
	// Setup HTTP server
	// ------------------------
	serverMux := http.NewServeMux()
	go func() {
		log.Println("Server listening on port: " + internalPort)
		log.Fatal(http.ListenAndServe(":"+internalPort, serverMux))
	}()
	auth.InitSigning(serverMux)
	InitializeKubernetes(serverMux)
	configurationData := startConfigurationEndpoint(serverMux)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	<-stop // wait for shutdown signal
	log.Println("Shutting down gracefully...")

	// ------------------------
	// 1. Stop all actors tracked in ConfigurationData
	// ------------------------
	for _, actor := range configurationData.actors {
		actor.Stop()
	}

	// ------------------------
	// 2. Remove remaining pods
	// ------------------------
	pods, err := Clientset.CoreV1().Pods("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Fatal(err)
	}
	for _, pod := range pods.Items {
		err := Clientset.CoreV1().Pods(pod.Namespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
		if err != nil {
			log.Printf("Failed to delete pod %s/%s: %v", pod.Namespace, pod.Name, err)
		} else {
			log.Printf("Deleted pod: %s/%s", pod.Namespace, pod.Name)
		}
	}

	// ------------------------
	// 3. Remove remaining services
	// ------------------------
	services, err := Clientset.CoreV1().Services("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		log.Fatal(err)
	}
	for _, svc := range services.Items {
		if svc.Name == "kubernetes" {
			continue
		}
		err := Clientset.CoreV1().Services(svc.Namespace).Delete(context.Background(), svc.Name, metav1.DeleteOptions{})
		if err != nil {
			log.Printf("Failed to delete service %s/%s: %v", svc.Namespace, svc.Name, err)
		} else {
			log.Printf("Deleted service: %s/%s", svc.Namespace, svc.Name)
		}
	}

	log.Println("Cleanup complete. Exiting.")
	// auth.DeleteAllResources()
}
