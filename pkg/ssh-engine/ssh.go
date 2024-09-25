package engine

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"k8s.io/klog/v2"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"k8s.io/apimachinery/pkg/util/wait"
)

const scriptWrapper = `#!/bin/sh
set -e
%s
`

type ShellCommandTemp struct {
	Cmds        []string `json:"Cmds"`
	TryCount    int      `json:"TryCount"`
	IsAsync     bool     `json:"IsAsync"`
	TempName    string   `json:"TempName"`
	Description string   `json:"Description"`
	ConditionOn []string `json:"ConditionOn"`
	//	support variable: AllNode, Manual
	ProcessingType    string   `json:"ProcessingType"`
	ProcessingNodeIps []string `json:"ProcessingNodeIps"`
	ProcessingNodeIds []string `json:"ProcessingNodeIds"`
}

// ExecShellCmds exec shell commands
func (sc *ShellCommandTemp) exec(n *Node, e *Engine) (err error) {
	realCmds, err := sc.ReplaceCommandVariable(n, e)
	if err != nil {
		return err
	}
	err = sc.buildTempLogWrite(n, e)
	if err != nil {
		return err
	}
	for _, cmd := range realCmds {
		for i := 0; i < sc.TryCount; i++ {
			klog.Infof("### %s execute [[ %s ]]", n.HostIp, cmd)
			cmdRlt, err := n.Dialer.ExecuteCommands(cmd)
			cmdRlt = "### " + n.HostIp + " execute [[ " + cmd + " ]]" + "result :\n" + cmdRlt + "\n"
			e.LogWriteLock.Lock()
			n.TempLogFileWriter[sc.TempName].Write([]byte(cmdRlt))
			e.LogWriteLock.Unlock()
			if err != nil {
				klog.Errorf("%v", err)
				continue
			}
			break
		}
	}
	return
}

func (sc *ShellCommandTemp) buildTempLogWrite(n *Node, e *Engine) (err error) {
	n.TempLogFileWriter[sc.TempName] = n.NodeLogFileWriter
	if e.LogScope == "ALL" || e.LogScope == "TEMP" {
		err = os.Mkdir(e.LogFilePath+"/"+n.HostIp, os.ModePerm)
		if err != nil && !os.IsExist(err) {
			klog.Error(err)
			return err
		}
		f, err := os.OpenFile(e.LogFilePath+"/"+n.HostIp+"/"+strconv.Itoa(len(n.TempLogFileWriter))+"-"+sc.TempName+".log", e.LogFileAttr, os.ModePerm)
		if err != nil {
			klog.Error(err)
			return err
		}
		if n.TempLogFileWriter[sc.TempName] == nil {
			n.TempLogFileWriter[sc.TempName] = f
		} else {
			n.TempLogFileWriter[sc.TempName] = io.MultiWriter(n.TempLogFileWriter[sc.TempName], f)
		}
	}
	n.TempLogFileWriter[sc.TempName] = io.MultiWriter(n.TempLogFileWriter[sc.TempName], os.Stdout)
	return nil
}

// ReplaceCommandVariable replace variable in command
func (sc *ShellCommandTemp) ReplaceCommandVariable(n *Node, e *Engine) (realCmds []string, err error) {
	realCmds = make([]string, len(sc.Cmds))
	copy(realCmds, sc.Cmds)
	for i, cmd := range realCmds {
		realCmds[i] = os.Expand(cmd, func(s string) string {
			//	if variable is not start with [ and end with ], then return itself
			if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
				return "${" + s + "}"
			}
			//	Intercept string, remove [ and ]
			s = s[1 : len(s)-1]
			//	Get variable value from os environment
			if getenv := os.Getenv(s); getenv != "" {
				return getenv
			}
			//	Get variable value from Node.Label
			if val, ok := n.Label[s]; ok {
				return val
			}
			//	Get variable value from Node
			if _, b1 := reflect.TypeOf(n).Elem().FieldByName(s); b1 {
				return reflect.ValueOf(n).Elem().FieldByName(s).String()
			}
			//	Get variable value from CustomConfigParams
			if val, ok := e.CustomConfigParams[s]; ok {
				return val
			}
			err = fmt.Errorf("variable %s not found", s)
			panic(err)
		})
		if err != nil {
			return
		}
	}
	return
}

type Node struct {
	Id                string               `json:"Id"`
	HostIp            string               `json:"HostIp"`
	SSHUsername       string               `json:"SSHUsername"`
	SSHPassword       string               `json:"SSHPassword"`
	SSHPort           int                  `json:"SSHPort"`
	Dialer            *SSHDialer           `json:"-"`
	Label             map[string]string    `json:"Label"`
	NodeLogFileWriter io.Writer            `json:"-"`
	TempLogFileWriter map[string]io.Writer `json:"-"`
}

var defaultBackoff = wait.Backoff{
	Duration: 60 * time.Second,
	Factor:   1,
	Steps:    5,
}

type SSHDialer struct {
	sshAddress string
	username   string
	password   string
	conn       *ssh.Client
	uid        int
}

// NewSSHDialer returns new ssh dialer.
func NewSSHDialer(username, password, sshAddress string, port int, timeout bool) (*SSHDialer, error) {

	d := &SSHDialer{
		username:   username,
		password:   password,
		sshAddress: fmt.Sprintf("%s:%d", sshAddress, port),
		uid:        -1,
	}

	try := 0
	if err := wait.ExponentialBackoff(defaultBackoff, func() (bool, error) {
		try++
		klog.Infof("the %d/%d time tring to ssh to %s with user %s", try, defaultBackoff.Steps, d.sshAddress, d.username)

		c, err := d.Dial(timeout)
		if err != nil {
			klog.Errorf("failed to dial ssh %s with user %s, error: %v", d.sshAddress, d.username, err)
			return false, nil
		}

		d.conn = c

		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("[ssh-dialer] init dialer [%s] error: %w", d.sshAddress, err)
	}

	return d, nil
}

// Dial handshake with ssh address.
func (d *SSHDialer) Dial(t bool) (*ssh.Client, error) {
	timeout := defaultBackoff.Duration
	if !t {
		timeout = 0
	}

	cfg, err := GetSSHConfig(d.username, d.password, timeout)
	if err != nil {
		klog.Errorf("failed to get ssh config, error: %v", err)
		return nil, err
	}
	// establish connection with SSH server.
	return ssh.Dial("tcp", d.sshAddress, cfg)
}

func (d *SSHDialer) getUserID() error {
	if d.uid >= 0 {
		return nil
	}
	session, err := d.conn.NewSession()
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()

	output, err := session.Output("id -u")
	if err != nil {
		return fmt.Errorf("failed to get current user id from remote host %s, %v", d.sshAddress, err)
	}
	// it should return a number with user id if ok
	d.uid, err = strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return fmt.Errorf("failed to parse uid output from remote host, output: %s, %v", string(output), err)
	}
	return nil
}

func (d *SSHDialer) Close() error {
	if d.conn != nil {
		return d.conn.Close()
	}
	return nil
}

func (d *SSHDialer) wrapCommands(cmd string) string {
	return base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(scriptWrapper, cmd)))
}

func (d *SSHDialer) ExecuteCommands(cmds ...string) (string, error) {
	if err := d.getUserID(); err != nil {
		return "", err
	}

	sudo := ""
	if d.uid > 0 {
		sudo = "sudo"
	}

	session, err := d.conn.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	encodedCMD := d.wrapCommands(strings.Join(cmds, "\n"))
	cmd := fmt.Sprintf("echo \"%s\" | base64 -d | %s bash -", encodedCMD, sudo)

	output := bytes.NewBuffer([]byte{})
	combinedOutput := singleWriter{
		b: output,
	}
	session.Stderr = &combinedOutput
	session.Stdout = &combinedOutput
	err = session.Run(cmd)
	return output.String(), err
}

// GetSSHConfig generate ssh config.
func GetSSHConfig(username, password string, timeout time.Duration) (*ssh.ClientConfig, error) {
	config := &ssh.ClientConfig{
		User:            username,
		Timeout:         timeout,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	if password != "" {
		config.Auth = append(config.Auth, ssh.Password(password))
	}
	return config, nil
}

type singleWriter struct {
	b  io.Writer
	mu sync.Mutex
}

func (w *singleWriter) Write(p []byte) (int, error) {
	//klog.Infof(string(p))
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}
