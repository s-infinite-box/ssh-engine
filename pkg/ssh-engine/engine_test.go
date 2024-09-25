package engine

import (
	"k8s.io/klog/v2"
	"testing"
	"time"
)

func TestInitCluster(t *testing.T) {
	err := Control("rke2_ovn_multus_kubevirt_scaleio.yaml")
	if err != nil {
		panic(err)
	}
}

func Test1(t *testing.T) {
	n := &Node{}
	t.Log(n)
}

func Test2(t *testing.T) {
	ch := make(chan string, 1)
	arr := []string{"a", "b", "c"}
	go func() {
		time.Sleep(1 * time.Second)
		for _, v := range arr {
			t.Log(v)
		}
		ch <- "done"
	}()
	arr = []string{"d", "e", "f"}
	<-ch
	t.Log(arr)
}

func TestSSH(t *testing.T) {
	dialer, err := NewSSHDialer("root", "", "172.26.0.2", 22, false)
	if err != nil {
		panic(err)
	}
	defer dialer.Close()
	c, e := dialer.ExecuteCommands("ip -br a")
	if e != nil {
		panic(e)
	}
	t.Log(c)
}

func Test_klog(t *testing.T) {
	klog.Info("abc")
	t.Log(11)
}
