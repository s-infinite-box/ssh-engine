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
	"time"
)

func Control(configPath string) error {
	//  build engineTemp
	engineTemp, err := BuildEngineTemp(configPath)

	if err != nil {
		return err
	}

	//  finally close ssh dialer
	defer func(e *Engine) {
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

var (
	LogScopes = []string{"ALL", "ONE", "NODE", "TEMP"}
)

type Engine struct {
	//  wait for all asynchronous task done
	WG sync.WaitGroup `json:"-"`
	//	k tempName v-k hostIp v-v chan
	TaskChan           map[string]map[string]chan string `json:"-"`
	TaskConditionNum   map[string]int                    `json:"-"`
	LogFileAttr        int                               `json:"-"`
	TotalLogFileWriter io.Writer                         `json:"-"`
	LogWriteLock       sync.Mutex                        `json:"-"`

	//	engine config
	LogFilePath string `json:"LogFilePath"`
	LogScope    string `json:"LogScope"`

	CustomConfigParams map[string]string `json:"CustomConfigParams"`
	//	exec node
	Nodes []*Node `json:"Nodes"`
	//	shell-command
	ShellCommandTempConfig []*ShellCommandTemp `json:"ShellCommandTempConfig"`
}

// BuildEngineTemp build Engine by yaml file
// the method only processing config
func BuildEngineTemp(configPath string) (*Engine, error) {
	RealConfigPath := configPath
	if RealConfigPath == "" {
		RealConfigPath = os.Getenv("ENGINE_CONFIG_PATH")
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
	var engineTemp Engine
	err = s.Decode(&engineTemp)
	if err != nil {
		return nil, err
	}
	//	init task chan
	engineTemp.TaskChan = make(map[string]map[string]chan string)

	//	init log file path
	if engineTemp.LogFilePath == "" {
		engineTemp.LogFilePath = LogFilePath + "_" + time.Now().Format("2006-01-02-15-04-05")
	}

	//	init log scope
	check := true
	if engineTemp.LogScope != "" {
		engineTemp.LogScope = strings.ToUpper(engineTemp.LogScope)
		for _, l := range LogScopes {
			if engineTemp.LogScope == l {
				check = false
				break
			}
		}
	}
	if check {
		engineTemp.LogScope = "ALL"
	}

	//	init log file attr
	engineTemp.LogFileAttr = os.O_CREATE | os.O_TRUNC | os.O_RDWR

	//	create log file path
	err = os.Mkdir(engineTemp.LogFilePath, os.ModePerm)
	if err != nil && !os.IsExist(err) {
		klog.Errorf("mkdir logPath %s failed, error: %v", engineTemp.LogFilePath, err)
		return nil, err
	}
	//	total log
	if engineTemp.LogScope == "ALL" || engineTemp.LogScope == "ONE" {
		engineTemp.TotalLogFileWriter, err = os.OpenFile(engineTemp.LogFilePath+"/total.log", engineTemp.LogFileAttr, os.ModePerm)
		if err != nil {
			klog.Error(err)
			return nil, err
		}
	}

	//	build ssh dialer and log file writer
	for _, node := range engineTemp.Nodes {
		//	create ssh dialer
		dialer, err := NewSSHDialer(node.SSHUsername, node.SSHPassword, node.HostIp, node.SSHPort, true)
		if err != nil {
			klog.Error(err)
			return nil, err
		}
		node.Dialer = dialer

		//	node log
		node.TempLogFileWriter = make(map[string]io.Writer)
		node.NodeLogFileWriter = engineTemp.TotalLogFileWriter
		if engineTemp.LogScope == "ALL" || engineTemp.LogScope == "NODE" {
			err = os.Mkdir(engineTemp.LogFilePath+"/"+node.HostIp, os.ModePerm)
			if err != nil && !os.IsExist(err) {
				klog.Error(err)
				return nil, err
			}
			f, err := os.OpenFile(engineTemp.LogFilePath+"/"+node.HostIp+"/"+node.HostIp+".log", engineTemp.LogFileAttr, os.ModePerm)
			if err != nil {
				klog.Error(err)
				return nil, err
			}
			if node.NodeLogFileWriter == nil {
				node.NodeLogFileWriter = f
			} else {
				node.NodeLogFileWriter = io.MultiWriter(f, node.NodeLogFileWriter)
			}
		}
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

func (engineTemp *Engine) ExecShellCmds() {
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
		//	select node by ProcessingType
		switch sct.ProcessingType {
		case "AllNode":
			ProcessNode = engineTemp.Nodes
		case "Manual":
			if len(sct.ProcessingNodeIps) > 0 {
				for _, ip := range sct.ProcessingNodeIps {
					for _, node := range engineTemp.Nodes {
						if node.HostIp == strings.ReplaceAll(ip, " ", "") {
							ProcessNode = append(ProcessNode, node)
						}
					}
				}
			} else if len(sct.ProcessingNodeIds) > 0 {
				for _, id := range sct.ProcessingNodeIds {
					for _, node := range engineTemp.Nodes {
						if node.Id == strings.ReplaceAll(id, " ", "") {
							ProcessNode = append(ProcessNode, node)
						}
					}
				}
			} else {
				panic("ProcessingType is Manual and It is empty what ProcessingNodeIps and ProcessingNodeIds ")
			}
			if len(sct.ProcessingNodeIps) != len(ProcessNode) && len(sct.ProcessingNodeIds) != len(ProcessNode) {
				klog.Warningf("The number of IPs in ProcessingNodeIps is not equal to the number of nodes")
			}
		default:
			klog.Errorf("Unsupported ProcessingType: %s", sct.ProcessingType)
			panic("Unsupported ProcessingType")
		}
		//klog.Infof("ProcessingType: %s, ProcessingNodeIps: %v, Async: %t ,Cmds: %v", sct.ProcessingType, sct.ProcessingNodeIps, sct.IsAsync, strings.Join(sct.Cmds, "\n"))
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
			if err := sct.exec(node, engineTemp); err != nil {
				klog.Errorf("%v", err)
			}
		}
	}
}

func (engineTemp *Engine) asyncExec(n *Node, s *ShellCommandTemp, chCap int) {
	defer engineTemp.WG.Done()
	if err := s.exec(n, engineTemp); err != nil {
		klog.Errorf("%v", err)
	}
	for i := 0; i < chCap; i++ {
		engineTemp.TaskChan[s.TempName][n.HostIp] <- n.HostIp + " " + s.Description + " done"
	}

}
