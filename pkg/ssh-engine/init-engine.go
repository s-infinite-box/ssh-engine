package engine

import (
	"bytes"
	"fmt"
	"io"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/klog/v2"
	"os"
	"strings"
	"sync"
)

func Control(configPath string) error {
	//  build engineTemp
	engineTemp, err := BuildEngineTemp(configPath)

	if err != nil {
		return err
	}

	//  finally close ssh dialer
	defer func(e *ShellCmdsEngineTemp) {
		for _, node := range e.Nodes {
			err := node.Dialer.Close()
			if err != nil {
				klog.Error(err)
			}
		}
		for _, NodeTaskChan := range e.TaskChan {
			for _, ch := range NodeTaskChan {
				close(ch)
			}
		}
	}(engineTemp)

	//  exec shell commands
	engineTemp.ExecShellCmds()

	//  wait for all asynchronous task done
	engineTemp.WG.Wait()

	return nil
}

const (
	ConfigPath  = "EngineTemp.yaml"
	LogFilePath = "log"
)

type ShellCmdsEngineTemp struct {
	//  wait for all asynchronous task done
	WG sync.WaitGroup `json:"-"`
	//	k tempName v-k hostIp v-v chan
	TaskChan           map[string]map[string]chan string `json:"-"`
	TaskConditionNum   map[string]int                    `json:"-"`
	CustomConfigParams map[string]string                 `json:"CustomConfigParams"`
	LogFilePath        string                            `json:"LogFilePath"`
	LogLevel           string                            `json:"LogLevel"`
	Nodes              []*Node                           `json:"Nodes"`
	//	shell-command
	ShellCommandTempConfig []*ShellCommandTemp `json:"ShellCommandTempConfig"`
}

// BuildEngineTemp build ShellCmdsEngineTemp by yaml file
// the method only processing config, not
func BuildEngineTemp(configPath string) (*ShellCmdsEngineTemp, error) {
	RealConfigPath := configPath
	if RealConfigPath == "" {
		RealConfigPath = os.Getenv("Engine_CONFIG_PATH")
	}
	if RealConfigPath == "" {
		RealConfigPath = ConfigPath
	}
	//	read ShellCommandTempConfig.yaml
	file, err := os.ReadFile(RealConfigPath)
	if err != nil {
		return nil, err
	}
	//	decode yaml file
	s := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(file), 100)
	var engineTemp ShellCmdsEngineTemp
	err = s.Decode(&engineTemp)
	if err != nil {
		return nil, err
	}
	//	init task chan
	engineTemp.TaskChan = make(map[string]map[string]chan string)
	realLogFilePath := engineTemp.LogFilePath
	if realLogFilePath == "" {
		realLogFilePath = LogFilePath
	}
	//	build ssh dialer and log file writer
	os.Mkdir(realLogFilePath, os.ModePerm)
	logFileAttr := os.O_CREATE | os.O_TRUNC | os.O_RDWR
	tf, err := os.OpenFile(realLogFilePath+"/totle.log", logFileAttr, os.ModePerm)
	for _, node := range engineTemp.Nodes {
		//	create ssh dialer
		dialer, err := NewSSHDialer(node.SSHUsername, node.SSHPassword, node.HostIp, node.SSHPort, true)
		if err != nil {
			klog.Error(err)
			return nil, err
		}
		node.Dialer = dialer
		os.Mkdir(realLogFilePath+"/"+node.HostIp, os.ModePerm)
		f, err := os.OpenFile(realLogFilePath+"/"+node.HostIp+"/"+node.HostIp+".log", logFileAttr, os.ModePerm)
		if err != nil {
			klog.Error(err)
			return nil, err
		}
		node.LogFileWriter = io.MultiWriter(os.Stdout, f, tf)
	}
	//
	engineTemp.TaskConditionNum = make(map[string]int)
	for _, sct := range engineTemp.ShellCommandTempConfig {
		for ci, c := range sct.ConditionOn {
			if _, ok := engineTemp.TaskConditionNum[c]; !ok {
				flag := true
				for _, s := range engineTemp.ShellCommandTempConfig {
					if s.TempName == c {
						engineTemp.TaskConditionNum[c] = 0
						flag = false
						continue
					}
				}
				if flag {
					panic(fmt.Sprintf("ConditionOn[%d] %s not found", ci, c))
				}
			}
			engineTemp.TaskConditionNum[c]++
		}
	}
	return &engineTemp, nil
}

func (engineTemp *ShellCmdsEngineTemp) ExecShellCmds() {
	for _, sct := range engineTemp.ShellCommandTempConfig {
		for _, condition := range sct.ConditionOn {
			if task, ok := engineTemp.TaskChan[condition]; ok {
				for _, ch := range task {
					v := <-ch
					klog.Infof("%s", v)
				}
			}
		}
		ProcessNode := make([]*Node, 0)
		switch sct.ProcessingType {
		case "AllNode":
			ProcessNode = engineTemp.Nodes
		case "Manual":
			for _, ip := range sct.ProcessingNodeIps {
				for _, node := range engineTemp.Nodes {
					if node.HostIp == strings.ReplaceAll(ip, " ", "") {
						ProcessNode = append(ProcessNode, node)
					}
				}
			}
			if len(sct.ProcessingNodeIps) != len(ProcessNode) {
				klog.Warningf("The number of IPs in ProcessingNodeIps is not equal to the number of nodes")
			}
		default:
			klog.Errorf("Unsupported ProcessingType: %s", sct.ProcessingType)
			panic("Unsupported ProcessingType")
		}
		klog.Infof("ProcessingType: %s, ProcessingNodeIps: %v, Async: %t ,Cmds: %v", sct.ProcessingType, sct.ProcessingNodeIps, sct.IsAsync, sct.Cmds)
		for _, node := range ProcessNode {
			//	Async exec
			if sct.IsAsync {
				if m, ok := engineTemp.TaskChan[sct.TempName]; !ok || m == nil {
					engineTemp.TaskChan[sct.TempName] = make(map[string]chan string)
				}
				chCap := 1
				if num, ok := engineTemp.TaskConditionNum[sct.TempName]; ok {
					chCap = num
				}
				ch := make(chan string, chCap)
				engineTemp.TaskChan[sct.TempName][node.HostIp] = ch
				engineTemp.WG.Add(1)
				go engineTemp.asyncExec(node, sct, chCap)
				continue
			}
			//	Sync exec
			if _, err := sct.exec(node, engineTemp); err != nil {
				klog.Errorf("%v", err)
			}
		}
	}
}

func (engineTemp *ShellCmdsEngineTemp) asyncExec(n *Node, s *ShellCommandTemp, chCap int) {
	defer engineTemp.WG.Done()
	if _, err := s.exec(n, engineTemp); err != nil {
		klog.Errorf("%v", err)
	}
	for i := 0; i < chCap; i++ {
		engineTemp.TaskChan[s.TempName][n.HostIp] <- n.HostIp + " " + s.Description + " done"
	}

}
