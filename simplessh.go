/*
	A simple abstraction around ssh and sftp libraries in go that extends sfreiberg's simplessh
	package to include ssh-agent support and separate output steams for command execution.
*/
package simplessh

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const DefaultTimeout = 30 * time.Second

type Client struct {
	SSHClient *ssh.Client
}

// Connect with a password. If username is empty simplessh will attempt to get the current user.
func ConnectWithPassword(host, username, pass string) (*Client, error) {
	return ConnectWithPasswordTimeout(host, username, pass, DefaultTimeout)
}

// Same as ConnectWithPassword but allows a custom timeout. If username is empty simplessh will attempt to get the current user.
func ConnectWithPasswordTimeout(host, username, pass string, timeout time.Duration) (*Client, error) {
	authMethod := ssh.Password(pass)

	return connect(username, host, authMethod, timeout)
}

// Connect with a private key. If privKeyPath is an empty string it will attempt
// to use $HOME/.ssh/id_rsa. If username is empty simplessh will attempt to get the current user.
func ConnectWithKeyFileTimeout(host, username, privKeyPath string, timeout time.Duration) (*Client, error) {
	if privKeyPath == "" {
		currentUser, err := user.Current()
		if err == nil {
			privKeyPath = filepath.Join(currentUser.HomeDir, ".ssh", "id_rsa")
		}
	}

	privKey, err := ioutil.ReadFile(privKeyPath)
	if err != nil {
		return nil, err
	}

	return ConnectWithKeyTimeout(host, username, string(privKey), timeout)
}

// Same as ConnectWithKeyFile but allows a custom timeout. If username is empty simplessh will attempt to get the current user.
func ConnectWithKeyFile(host, username, privKeyPath string) (*Client, error) {
	return ConnectWithKeyFileTimeout(host, username, privKeyPath, DefaultTimeout)
}

// Connect with a private key with a custom timeout. If username is empty simplessh will attempt to get the current user.
func ConnectWithKeyTimeout(host, username, privKey string, timeout time.Duration) (*Client, error) {
	signer, err := ssh.ParsePrivateKey([]byte(privKey))
	if err != nil {
		return nil, err
	}

	authMethod := ssh.PublicKeys(signer)

	return connect(username, host, authMethod, timeout)
}

// Connect with a private key. If username is empty simplessh will attempt to get the current user.
func ConnectWithKey(host, username, privKey string) (*Client, error) {
	return ConnectWithKeyTimeout(host, username, privKey, DefaultTimeout)
}

// Connect with a ssh agent with a custom timeout. If username is empty simplessh will attempt to get the current user.
func ConnectWithSshAgentTimeout(host, username string, timeout time.Duration) (*Client, error) {
	sshAgent, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
	if err != nil {
		return nil, err
	}
	authMethod := ssh.PublicKeysCallback(agent.NewClient(sshAgent).Signers)
	return connect(username, host, authMethod, timeout)
}

// Connect with a ssh agent. If username is empty simplessh will attempt to get the current user.
func ConnectWithSshAgent(host, username string) (*Client, error) {
	return ConnectWithSshAgentTimeout(host, username, DefaultTimeout)
}

func connect(username, host string, authMethod ssh.AuthMethod, timeout time.Duration) (*Client, error) {
	if username == "" {
		user, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("Username wasn't specified and couldn't get current user: %v", err)
		}

		username = user.Username
	}

	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{authMethod},
	}

	host = addPortToHost(host)

	conn, err := net.DialTimeout("tcp", host, timeout)
	if err != nil {
		return nil, err
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, host, config)
	if err != nil {
		return nil, err
	}
	client := ssh.NewClient(sshConn, chans, reqs)

	c := &Client{SSHClient: client}
	return c, nil
}

// Execute cmd on the remote host and return stderr and stdout combined
func (c *Client) Exec(cmd string) ([]byte, error) {
	session, err := c.SSHClient.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()

	return session.CombinedOutput(cmd)
}

// Execute cmd on the remote host and return stderr and stdout as separte streams
func (c *Client) ExecWithOutputStreams(cmd string) ([]byte, []byte, error) {
	session, err := c.SSHClient.NewSession()
	if err != nil {
		return nil, nil, err
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer

	session.Stdout = &stdout
	session.Stderr = &stderr

	err = session.Run(cmd)

	return stdout.Bytes(), stderr.Bytes(), err
}

func (c *Client) Download(remote, local string) error {
	client, err := sftp.NewClient(c.SSHClient)
	if err != nil {
		return err
	}
	defer client.Close()

	remoteFile, err := client.Open(remote)
	if err != nil {
		return err
	}
	defer remoteFile.Close()

	localFile, err := os.Create(local)
	if err != nil {
		return err
	}
	defer localFile.Close()

	_, err = io.Copy(localFile, remoteFile)
	return err
}

func (c *Client) Upload(local, remote string) error {
	client, err := sftp.NewClient(c.SSHClient)
	if err != nil {
		return err
	}
	defer client.Close()

	localFile, err := os.Open(local)
	if err != nil {
		return err
	}
	defer localFile.Close()

	remoteFile, err := client.Create(remote)
	if err != nil {
		return err
	}

	_, err = io.Copy(remoteFile, localFile)
	return err
}

// Read a remote file and return the contents.
func (c *Client) ReadAll(filepath string) ([]byte, error) {
	sftp, err := c.SFTPClient()
	if err != nil {
		panic(err)
	}
	defer sftp.Close()

	file, err := sftp.Open(filepath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return ioutil.ReadAll(file)
}

// Close the underlying SSH connection
func (c *Client) Close() error {
	return c.SSHClient.Close()
}

// Return an sftp client. The client needs to be closed when it's no
// longer needed.
func (c *Client) SFTPClient() (*sftp.Client, error) {
	return sftp.NewClient(c.SSHClient)
}

func addPortToHost(host string) string {
	_, _, err := net.SplitHostPort(host)

	// We got an error so blindly try to add a port number
	if err != nil {
		return net.JoinHostPort(host, "22")
	}

	return host
}
