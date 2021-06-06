package annotation

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
        "time"
        "context"
        //"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types/current"
         "k8s.io/apimachinery/pkg/util/wait"
	"github.com/vishvananda/netlink"
        "k8s.io/apimachinery/pkg/api/errors"
	weaveapi "github.com/weaveworks/weave/api"
        metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
        corev1 "k8s.io/api/core/v1"
        "k8s.io/client-go/kubernetes"
        "k8s.io/client-go/tools/clientcmd"
)
const (
	K8S_POD_NAMESPACE          = "K8S_POD_NAMESPACE"
	K8S_POD_NAME               = "K8S_POD_NAME"
	K8S_POD_INFRA_CONTAINER_ID = "K8S_POD_INFRA_CONTAINER_ID"
        ExtendedCNIArgsAnnotation = "k8s.v1.cni.galaxy.io/args"

	stateDir                   = "/var/lib/cni/galaxy/port"
	PortMappingPortsAnnotation = "tkestack.io/portmapping"
)
type NetworkInfo struct {
        NetworkType string
        Args        map[string]string
        Conf        map[string]interface{}
        IfName      string
}

var (
	zeroNetwork = net.IPNet{IP: net.IPv4zero, Mask: net.IPv4Mask(0, 0, 0, 0)}
	mask32      = net.IPv4Mask(0xff, 0xff, 0xff, 0xff)
)
type IPInfo struct {
	IP      string `json:"ip"`
	Vlan    uint16      `json:"vlan"`
	Gateway net.IP      `json:"gateway"`
}

type IPLIST struct {
    Ipinfos  []*IPInfo
}

type CNIPlugin struct {
	weave *weaveapi.Client
}


func NewCNIPlugin(weave *weaveapi.Client) *CNIPlugin {
	return &CNIPlugin{weave: weave}
}

func IPInfoToResult(iplist []IPInfo) *current.Result {
        newResult := &current.Result{}
        for _,v := range(iplist) { 
            ipnet,_ := netlink.ParseIPNet(v.IP)
            newResult.IPs = append(newResult.IPs, &current.IPConfig{
	    	Version: "4",
                Address: *ipnet,
             })
	}
	return newResult
}

func parseExtendedCNIArgs(pod *corev1.Pod) (map[string]json.RawMessage, error) {
	if pod.Annotations == nil {
		return nil, nil
	}
	args := pod.Annotations[ExtendedCNIArgsAnnotation]
	if args == "" {
		return nil, nil
	}
	// CniArgs is the cni args in pod annotation
	var cniArgs struct {
		// Common is the common args for cni plugins to setup network
		Common map[string]json.RawMessage `json:"common"`
	}
	if err := json.Unmarshal([]byte(args), &cniArgs); err != nil {
        	logOnStderr(fmt.Errorf("getpodxxxxxxxxxxxxxxxxxxxxxx", err))
		return nil, fmt.Errorf("failed to unmarshal cni args %s: %v", args, err)
	}
        logOnStderr(fmt.Errorf("getpodxxxxxxxxxxxxxxxxxxxxxx", cniArgs))
	return cniArgs.Common, nil
}

func ParseCNIArgs(args string) (map[string]string, error) {
        kvMap := make(map[string]string)
        kvs := strings.Split(args, ";")
        if len(kvs) == 0 {
                return kvMap, fmt.Errorf("invalid args %s", args)
        }
        for _, kv := range kvs {
                part := strings.SplitN(kv, "=", 2)
                if len(part) != 2 {
                        continue
                }
                kvMap[strings.TrimSpace(part[0])] = strings.TrimSpace(part[1])
        }
        return kvMap, nil
}

func  getIPFromAnnotation(kubeconfig string, args *skel.CmdArgs) (*current.Result, error) {
	var pod *corev1.Pod
        cniArgs, err := ParseCNIArgs(args.Args)
        if err != nil {
             return nil,err
        }
        PodNamespace, _ := cniArgs[K8S_POD_NAMESPACE]
        PodName, _ := cniArgs[K8S_POD_NAME]
	printOnce := false
        //config, err := rest.InClusterConfig()
        if kubeconfig != nil {
        	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
            client, err := kubernetes.NewForConfig(config)
        }
        logOnStderr(fmt.Errorf("get pods init configxxxxxxxxxxxxxxxxxxxxxx",err))
        logOnStderr(fmt.Errorf("get pods init configxxxxxxxxxxxxxxxxxxxxxx",err))
        if err != nil {
	     return nil,err
        }
	if err := wait.PollImmediate(time.Millisecond*500, 5*time.Second, func() (done bool, err error) {
		pod, err = client.CoreV1().Pods(PodNamespace).Get(context.TODO(), PodName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				if printOnce == false {
					printOnce = true
					fmt.Errorf("can't find pod %s_%s, retring", PodName, PodNamespace)
				}
				return false, nil
			}
			return false, err
		}
		return true, nil
	}); err != nil {
		return nil, fmt.Errorf("failed to get pod %s_%s: %v", PodName, namespace, err)
	}
        if err != nil {
                return nil,err
        }
        extendedCNIArgs, err := parseExtendedCNIArgs(pod)
        var infos []IPInfo
        for k, v := range extendedCNIArgs {
                if k == "ipinfos" {
                        if err:=json.Unmarshal(v, &infos);err!=nil {
                                logOnStderr(fmt.Errorf("getpod2xxxxxxxxxxxxxxxxxxxxxxargs",err))
                        }
                }
                logOnStderr(fmt.Errorf("getpod2xxxxxxxxxxxxxxxxxxxxxxargs",k, v))
        }
	   newResult := IPInfoToResult(infos)
       return newResult, nil
}

func logOnStderr(err error) {
	fmt.Fprintln(os.Stderr, "weave-cni:", err)
}
