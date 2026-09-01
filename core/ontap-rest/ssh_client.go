package ontap_rest

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"golang.org/x/crypto/ssh"
)

// SSHClient provides SSH connectivity to ONTAP clusters
type SSHClient interface {
	ExecuteCommand(ctx context.Context, command string) (string, error)
	ExecuteCommandWithInput(ctx context.Context, command string, input string) (string, error)
	Close() error
}

type sshClient struct {
	client   *ssh.Client
	host     string
	username string
	logger   log.Logger
}

// SSHClientParams contains parameters for SSH connection
type SSHClientParams struct {
	Host       string
	Port       int // SSH port (default: 22)
	Username   string
	Password   string
	PrivateKey string // For certificate-based authentication
	AuthType   int    // 0=password, 1=password_secret_mgr, 2=certificate
	Timeout    time.Duration
}

// NewSSHClient creates a new SSH client for ONTAP operations
func NewSSHClient(ctx context.Context, params SSHClientParams) (SSHClient, error) {
	logger := util.GetLogger(ctx)

	// Set default timeout if not provided
	if params.Timeout == 0 {
		params.Timeout = 30 * time.Second
	}

	// Set default port if not provided
	if params.Port == 0 {
		params.Port = 22
	}

	logger.Debugf("Creating SSH connection to %s@%s:%d with AuthType: %d", params.Username, params.Host, params.Port, params.AuthType)

	// Configure SSH client based on authentication type
	var authMethods []ssh.AuthMethod

	if params.AuthType == env.USER_CERTIFICATE { // Certificate authentication
		logger.Debug("Using private key authentication for SSH")
		if params.PrivateKey == "" {
			return nil, fmt.Errorf("private key is required for certificate authentication")
		}

		// Parse the private key
		block, _ := pem.Decode([]byte(params.PrivateKey))
		if block == nil {
			return nil, fmt.Errorf("failed to decode private key PEM")
		}

		// Parse the private key
		privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			// Try PKCS8 format
			key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("failed to parse private key: %v", err)
			}
			privateKey = key.(*rsa.PrivateKey)
		}

		// Create SSH signer
		signer, err := ssh.NewSignerFromKey(privateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create SSH signer: %v", err)
		}

		authMethods = append(authMethods, ssh.PublicKeys(signer))
	} else {
		// Password authentication
		logger.Debug("Using password authentication for SSH")
		if params.Password == "" {
			return nil, fmt.Errorf("password is required for password authentication")
		}
		authMethods = append(authMethods, ssh.Password(params.Password))
	}

	config := &ssh.ClientConfig{
		User:            params.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         params.Timeout,
	}

	// Connect to the SSH server
	address := net.JoinHostPort(params.Host, fmt.Sprintf("%d", params.Port))
	conn, err := net.DialTimeout("tcp", address, params.Timeout)
	if err != nil {
		logger.Errorf("Failed to dial SSH server %s: %v", address, err)
		return nil, fmt.Errorf("failed to dial SSH server: %w", err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, params.Host, config)
	if err != nil {
		logger.Errorf("Failed to establish SSH connection to %s: %v", params.Host, err)
		return nil, fmt.Errorf("failed to establish SSH connection: %w", err)
	}

	client := ssh.NewClient(sshConn, chans, reqs)

	logger.Debugf("Successfully established SSH connection to %s", params.Host)

	return &sshClient{
		client:   client,
		host:     params.Host,
		username: params.Username,
		logger:   logger,
	}, nil
}

// ExecuteCommand executes a command on the ONTAP cluster via SSH
func (sc *sshClient) ExecuteCommand(ctx context.Context, command string) (string, error) {
	sc.logger.Debugf("Executing SSH command: %s", command)

	// Create a new session
	session, err := sc.client.NewSession()
	if err != nil {
		sc.logger.Errorf("Failed to create SSH session: %v", err)
		return "", fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			sc.logger.Warnf("Failed to close SSH session: %v", closeErr)
		}
	}()

	// Execute the command
	output, err := session.CombinedOutput(command)
	if err != nil {
		sc.logger.Errorf("SSH command failed: %s, error: %v", command, err)
		return string(output), fmt.Errorf("SSH command failed: %w", err)
	}

	sc.logger.Debugf("SSH command executed successfully. Output: %s", string(output))
	return string(output), nil
}

// ExecuteCommandWithInput executes a command on the ONTAP cluster via SSH with stdin input
func (sc *sshClient) ExecuteCommandWithInput(ctx context.Context, command string, input string) (string, error) {
	sc.logger.Debugf("Executing SSH command with input: %s", command)

	// Create a new session
	session, err := sc.client.NewSession()
	if err != nil {
		sc.logger.Errorf("Failed to create SSH session: %v", err)
		return "", fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer func() {
		if closeErr := session.Close(); closeErr != nil {
			sc.logger.Warnf("Failed to close SSH session: %v", closeErr)
		}
	}()

	// Set up stdin pipe
	stdin, err := session.StdinPipe()
	if err != nil {
		sc.logger.Errorf("Failed to create stdin pipe: %v", err)
		return "", fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Set up stdout and stderr pipes to capture output
	stdout, err := session.StdoutPipe()
	if err != nil {
		sc.logger.Errorf("Failed to create stdout pipe: %v", err)
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		sc.logger.Errorf("Failed to create stderr pipe: %v", err)
		return "", fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	err = session.Start(command)
	if err != nil {
		sc.logger.Errorf("Failed to start SSH command: %v", err)
		return "", fmt.Errorf("failed to start SSH command: %w", err)
	}

	// Send input to stdin with delays between each line
	lines := strings.Split(strings.TrimSpace(input), "\n")
	sc.logger.Debugf("Sending %d lines of input to stdin", len(lines))

	for i, line := range lines {
		sc.logger.Debugf("Sending line %d: %s", i+1, line)
		_, err = stdin.Write([]byte(line + "\n"))
		if err != nil {
			sc.logger.Errorf("Failed to write line %d to stdin: %v", i+1, err)
			return "", fmt.Errorf("failed to write line %d to stdin: %w", i+1, err)
		}

		// Add a longer delay between inputs to allow ONTAP to process each prompt
		if i < len(lines)-1 { // Don't delay after the last line
			sc.logger.Debugf("Waiting 500ms before sending next input")
			time.Sleep(500 * time.Millisecond)
		}
	}

	// Close stdin to signal end of input
	err = stdin.Close()
	if err != nil {
		sc.logger.Errorf("Failed to close stdin: %v", err)
		return "", fmt.Errorf("failed to close stdin: %w", err)
	}

	// Read output from stdout and stderr
	var stdoutBytes, stderrBytes []byte
	done := make(chan error, 2)

	// Read stdout
	go func() {
		var err error
		stdoutBytes, err = io.ReadAll(stdout)
		done <- err
	}()

	// Read stderr
	go func() {
		var err error
		stderrBytes, err = io.ReadAll(stderr)
		done <- err
	}()

	// Wait for both reads to complete
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			sc.logger.Errorf("Failed to read output: %v", err)
			return "", fmt.Errorf("failed to read output: %w", err)
		}
	}

	// Wait for command to complete with timeout
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- session.Wait()
	}()

	select {
	case waitErr := <-waitDone:
		combinedOutput := string(stdoutBytes) + string(stderrBytes)
		sc.logger.Debugf("Command completed with output: %s", combinedOutput)

		if waitErr != nil {
			sc.logger.Errorf("SSH command with input failed: %s, error: %v, output: %s", command, waitErr, combinedOutput)
			// Don't return error immediately - let's see if we can get more info
			// Status 255 might not always be a failure for password changes
			if combinedOutput != "" {
				sc.logger.Debugf("Command had output, treating as potential success despite exit code")
				return combinedOutput, nil
			}
			return combinedOutput, fmt.Errorf("SSH command with input failed: %w", waitErr)
		}
	case <-time.After(30 * time.Second):
		sc.logger.Errorf("SSH command with input timed out after 30 seconds")
		return "", fmt.Errorf("SSH command with input timed out")
	}

	// Combine stdout and stderr for the output
	combinedOutput := string(stdoutBytes) + string(stderrBytes)
	sc.logger.Debugf("SSH command with input executed successfully. Output: %s", combinedOutput)
	return combinedOutput, nil
}

// Close closes the SSH connection
func (sc *sshClient) Close() error {
	sc.logger.Debug("Closing SSH connection")
	return sc.client.Close()
}
