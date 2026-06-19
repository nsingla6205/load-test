package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

func cleanupAdminSecret() {
	ns := os.Getenv("POD_NAMESPACE")
	if ns == "" {
		slog.Info("POD_NAMESPACE not set, skipping admin secret cleanup")
		return
	}

	// Secret name is configurable so the Temporal IAM job (which references
	// an operator-managed secret with a different name) can either point
	// cleanup at its own ephemeral secret or skip cleanup entirely by
	// setting IAM_LIFECYCLE_ADMIN_SECRET_NAME="-".
	secretName := envOr("IAM_LIFECYCLE_ADMIN_SECRET_NAME", "iam-lifecycle-admin-secret")
	if secretName == "-" {
		slog.Info("IAM_LIFECYCLE_ADMIN_SECRET_NAME=-, skipping admin secret cleanup")
		return
	}

	tokenBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		slog.Warn("cannot read SA token, skipping cleanup", "error", err)
		return
	}
	token := strings.TrimSpace(string(tokenBytes))

	caCert, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		slog.Warn("cannot read CA cert, skipping cleanup", "error", err)
		return
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caCert)

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: caPool},
		},
	}

	apiBase := fmt.Sprintf("https://kubernetes.default.svc/apis/external-secrets.io/v1beta1/namespaces/%s/externalsecrets/%s", ns, secretName)
	secretBase := fmt.Sprintf("https://kubernetes.default.svc/api/v1/namespaces/%s/secrets/%s", ns, secretName)

	for _, url := range []string{apiBase, secretBase} {
		req, err := http.NewRequest(http.MethodDelete, url, nil)
		if err != nil {
			slog.Warn("cleanup: build request failed", "url", url, "error", err)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			slog.Warn("cleanup: request failed", "url", url, "error", err)
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		slog.Info("cleanup: deleted", "url", url, "status", resp.StatusCode)
	}
}

func shutdownProxy() {
	for _, port := range proxyAdminPorts() {
		sendQuit(port)
	}
}

func proxyAdminPorts() []string {
	ports := []string{envOr("CLOUD_SQL_PROXY_ADMIN_PORT", "9091")}
	if p := os.Getenv("CLOUD_SQL_PROXY_TEMPORAL_ADMIN_PORT"); p != "" {
		ports = append(ports, p)
	}
	return ports
}

func sendQuit(port string) {
	addr := net.JoinHostPort("127.0.0.1", port)
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		slog.Warn("proxy shutdown: connect failed", "port", port, "error", err)
		return
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.Write([]byte("POST /quitquitquit HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 512)
	_, _ = conn.Read(buf)
	slog.Info("proxy shutdown signal sent", "port", port)
}
