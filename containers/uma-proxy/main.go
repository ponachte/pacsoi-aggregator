package main

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"time"
)

var caCert *x509.Certificate
var caKey *rsa.PrivateKey

// main initializes and starts the UMA proxy server.
// It launches two listeners:
//   - An HTTP proxy on port 8080.
//   - An HTTPS MITM proxy on port 8443 that intercepts TLS traffic using dynamically generated certificates.
//
// It also loads the internal CA certificate and key from environment variables for signing MITM certificates.
func main() {
	// Register the HTTP handler for incoming requests
	http.HandleFunc("/", Handler)

	// Start the HTTP proxy in a separate goroutine
	go func() {
		log.Println("HTTP proxy listening on port: 8080")
		log.Fatal(http.ListenAndServe(":8080", nil))
	}()

	// Load internal CA certificate and key from environment variables
	caCertPath := os.Getenv("CERT_PATH")
	caKeyPath := os.Getenv("KEY_PATH")

	var err error
	caCert, caKey, err = loadCA(caCertPath, caKeyPath)
	if err != nil {
		log.Fatalf("❌ Failed to load CA cert and key: %v", err)
	}

	// Start the HTTPS MITM proxy on port 8443
	ln, err := net.Listen("tcp", ":8443")
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()
	log.Println("🚀 HTTPS MITM proxy listening on port: 8443")

	// Accept incoming TLS connections and handle them with MITM logic
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("Accept error:", err)
			continue
		}
		go handleMITM(conn)
	}
}

// TODO add a cache to the proxy

// Handler is an HTTP reverse proxy handler that forwards incoming requests to an upstream service.
// It strips the "Authorization" header from the original request, forwards the modified request,
// and relays the upstream response back to the client.
//
// Parameters:
//
//	w   - the ResponseWriter used to send the response back to the client.
//	req - the incoming HTTP request.
func Handler(w http.ResponseWriter, req *http.Request) {
	// Log the incoming request method and URI
	fmt.Println("Request received", req.Method, req.RequestURI)

	// Create a new outbound request with the same method, URI, and body
	outReq, err := http.NewRequest(req.Method, req.RequestURI, req.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Copy all headers from the original request, except "Authorization"
	for key, value := range req.Header {
		if key == "Authorization" {
			continue
		}
		for _, element := range value {
			outReq.Header.Add(key, element)
		}
	}

	// Send the outbound request using the custom Do function
	resp, err := Do(outReq)
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers from the upstream response to the client response
	for key, value := range resp.Header {
		w.Header()[key] = value
	}
	// Write the upstream status code to the client
	w.WriteHeader(resp.StatusCode)
	// Stream the response body from the upstream service to the client
	io.Copy(w, resp.Body)
	// Log the response location and status
	location, _ := resp.Location()
	fmt.Println("Response", location, resp.Status)
}

// handleMITM intercepts HTTPS traffic using a CONNECT tunnel.
// It dynamically generates a TLS certificate for the target host,
// performs a TLS handshake with the client, decrypts the traffic,
// and forwards the request through the UMA authorization flow.
// The response from the upstream server is then sent back to the client.
//
// Parameters:
//
//	conn - the raw TCP connection from the client.
func handleMITM(conn net.Conn) {
	defer conn.Close()

	// Wrap the connection in a buffered reader to parse the initial HTTP CONNECT request
	connReader := bufio.NewReader(conn)
	req, err := http.ReadRequest(connReader)
	if err != nil {
		log.Println("❌ Failed to parse CONNECT request:", err)
		return
	}

	// Ensure the request is a CONNECT method (used for HTTPS tunneling)
	if req.Method != http.MethodConnect {
		log.Println("❌ Non-CONNECT request received on MITM listener, ignoring")
		return
	}

	// Extract the target host from the CONNECT request
	targetHost, _, err := net.SplitHostPort(req.Host)
	if err != nil {
		targetHost = req.Host // fallback if no port is specified
	}
	log.Println("🔌 Intercepting CONNECT to:", targetHost)

	// Respond to the client to establish the tunnel
	fmt.Fprint(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")

	// Generate a TLS certificate for the target host, signed by the internal CA
	certPEM, keyPEM, err := generateCert(targetHost)
	if err != nil {
		log.Println("❌ Failed to generate MITM cert:", err)
		return
	}

	// Load the generated certificate and key into a TLS certificate structure
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		log.Println("❌ X509KeyPair error:", err)
		return
	}

	// Create a TLS server using the generated certificate
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}
	tlsConn := tls.Server(conn, tlsConfig)

	// Perform the TLS handshake with the client
	if err := tlsConn.Handshake(); err != nil {
		log.Println("❌ TLS handshake error:", err)
		return
	}
	defer tlsConn.Close()

	// Read decrypted HTTPS requests from the client
	tlsReader := bufio.NewReader(tlsConn)
	for {
		req, err := http.ReadRequest(tlsReader)
		if err != nil {
			if err == io.EOF {
				return // client closed connection
			}
			log.Println("❌ Failed to read decrypted request:", err)
			return
		}

		// Prepare the request for forwarding
		req.URL.Scheme = "https"
		req.URL.Host = req.Host
		log.Println("➡️ MITM request:", req.Method, req.URL.String())

		// Create a new outbound request with the same method, URL, and body
		outReq, err := http.NewRequest(req.Method, req.URL.String(), req.Body)
		if err != nil {
			sendError(tlsConn, http.StatusBadRequest, "bad request")
			return
		}

		// Copy headers from the original request, excluding Authorization
		for key, value := range req.Header {
			if key == "Authorization" {
				continue
			}
			for _, element := range value {
				outReq.Header.Add(key, element)
			}
		}

		// Forward the request using the UMA authorization flow
		resp, err := Do(outReq)
		if err != nil {
			sendError(tlsConn, http.StatusBadGateway, "upstream error: "+err.Error())
			return
		}
		defer resp.Body.Close()

		// Write the upstream response back to the client
		err = resp.Write(tlsConn)
		if err != nil {
			log.Println("❌ Failed to write back to client:", err)
			return
		}
	}
}

// sendError writes a raw HTTP error response to the given writer.
//
// Parameters:
//
//	w          - the writer (e.g., TLS or TCP connection).
//	statusCode - the HTTP status code to send.
//	message    - the error message to include in the response body.
func sendError(w io.Writer, statusCode int, message string) {
	statusText := http.StatusText(statusCode)
	body := fmt.Sprintf("%d %s: %s", statusCode, statusText, message)
	fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\n\r\n%s",
		statusCode, statusText, len(body), body)
}

// loadCA loads a PEM-encoded X.509 certificate and a PKCS#8 RSA private key from the given file paths.
// These are used as the internal Certificate Authority (CA) for signing MITM certificates.
//
// Parameters:
//
//	certFile - path to the PEM-encoded certificate file.
//	keyFile  - path to the PEM-encoded PKCS#8 private key file.
//
// Returns:
//
//	*x509.Certificate - the parsed CA certificate.
//	*rsa.PrivateKey   - the parsed RSA private key.
//	error             - if any file reading, decoding, or parsing fails.
func loadCA(certFile, keyFile string) (*x509.Certificate, *rsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(certPEM)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, err
	}

	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, nil, err
	}
	block, _ = pem.Decode(keyPEM)
	parsedKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, ok := parsedKey.(*rsa.PrivateKey)
	if !ok {
		return nil, nil, fmt.Errorf("not an RSA private key")
	}
	if err != nil {
		return nil, nil, err
	}

	return cert, key, nil
}

// generateCert creates a TLS certificate for the specified host,
// signed by the internal CA certificate and key.
//
// Parameters:
//
//	host - the hostname for which to generate the certificate.
//
// Returns:
//
//	certPEM - PEM-encoded certificate.
//	keyPEM  - PEM-encoded RSA private key.
//	error   - if certificate generation fails.
func generateCert(host string) ([]byte, []byte, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName: host,
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().AddDate(1, 0, 0),
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
		DNSNames: []string{host},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, caCert, &priv.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	return certPEM, keyPEM, nil
}
