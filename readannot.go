package annotation

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	//"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/vishvananda/netlink"
	weaveapi "github.com/weaveworks/weave/api"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"tkestack.io/galaxy/pkg/utils/nets"
	pageutil "tkestack.io/galaxy/pkg/utils/page"
)

// FloatingIP is the floating ip info
type FloatingIP struct {
	IP                string            `json:"ip"`
	Namespace         string            `json:"namespace,omitempty"`
	AppName           string            `json:"appName,omitempty"`
	PodName           string            `json:"podName,omitempty"`
	PoolName          string            `json:"poolName,omitempty"`
	Policy            uint16            `json:"policy"`
	AppType           string            `json:"appType,omitempty"`
	UpdateTime        time.Time         `json:"updateTime,omitempty"`
	Status            string            `json:"status,omitempty"`
	Releasable        bool              `json:"releasable,omitempty"`
	labels            map[string]string `json:"-"`
	NetType           string            `json:"netType,omitempty"`
	AllocateNamespace string            `json:"allocateNamespace,omitempty"`
}

type FloatingIPPool struct {
	NodeSubnets []*net.IPNet // the node subnets
	nets.SparseSubnet
	sync.RWMutex
	nodeSubnets sets.String // the node subnets, string set format
	index       int         // the index of []FloatingIPPool
}
type ListIPResp struct {
	pageutil.Page
	Content []FloatingIP `json:"content,omitempty"`
}

const (
	K8S_POD_NAMESPACE          = "K8S_POD_NAMESPACE"
	K8S_POD_NAME               = "K8S_POD_NAME"
	K8S_POD_INFRA_CONTAINER_ID = "K8S_POD_INFRA_CONTAINER_ID"
	ExtendedCNIArgsAnnotation  = "k8s.v1.cni.galaxy.io/args"

	stateDir                   = "/var/lib/cni/galaxy/port"
	PortMappingPortsAnnotation = "tkestack.io/portmapping"
	GalaxyUrl                  = "http://galaxy-ipam.kube-system:9041"
)

type AlllocateResult struct {
	Code    string
	Message string
}

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
	Vlan    uint16 `json:"vlan"`
	Gateway net.IP `json:"gateway"`
}

type IPLIST struct {
	Ipinfos []*IPInfo
}

type CNIPlugin struct {
	weave *weaveapi.Client
}

func NewCNIPlugin(weave *weaveapi.Client) *CNIPlugin {
	return &CNIPlugin{weave: weave}
}

func IPInfoToResult(iplist []IPInfo) *current.Result {
	newResult := &current.Result{}
	for _, v := range iplist {
		ipnet, _ := netlink.ParseIPNet(v.IP)
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

func GetIpFromGalaxy(args *skel.CmdArgs) (*current.Result, error) {
	params := url.Values{}
	params.Add("namespace", "default")
	params.Add("netType", "overlay")
	params.Add("nodeip", string(GetOutboundIP())+"/32")
	params.Add("page", "1")
	params.Add("size", "1")
	requestUrlR := fmt.Sprintf(GalaxyUrl + "/v1/available/ip?" + params.Encode())
	//curl 'xxxx:xxxx/v1/allocation/ip?namespace=default&netType=overlay&ip=10.100.0.102&cid=weave-expose
	respR, err := http.Get(requestUrlR)
	if err != nil {
		logOnStderr(fmt.Errorf("get ip", err))
	}
	floatResp := new(ListIPResp)
	err = json.NewDecoder(respR.Body).Decode(&floatResp)
	if err != nil {
		logOnStderr(fmt.Errorf("get ip", err))
	}
	params = url.Values{}
	params.Add("namespace", "default")
	params.Add("netType", "overlay")
	params.Add("ip", floatResp.Content[0].IP)
	params.Add("cid", args.ContainerID)
	requestUrlS := fmt.Sprintf(GalaxyUrl + "/v1/allocation/ip?" + params.Encode())
	_, err = http.Get(requestUrlS)
	if err != nil {
		logOnStderr(fmt.Errorf("get ip", err))
	}
	Result := &current.Result{}
	Result.IPs = []*current.IPConfig{
		{
			Version: "4",
			Address: net.IPNet{IP: net.ParseIP(floatResp.Content[0].IP), Mask: net.IPv4Mask(255, 255, 255, 255)},
		},
	}
	return Result, err

}

func GetOrAllcateNodeIP(cid string) (*current.Result, error) {
	logOnStderr(fmt.Errorf("get ip start from galaxy ", cid))
	hname, err := os.Hostname()
	if err != nil {
		logOnStderr(fmt.Errorf("gethostname-error", err))
	}
	params := url.Values{}
	params.Add("namespace", "default")
	params.Add("netType", "overlay")
	params.Add("cid", cid)
	params.Add("hostname", hname)
	params.Add("nodeip", string(GetOutboundIP())+"/32")
	requestUrlS := fmt.Sprintf(GalaxyUrl + "/v1/checkorallocatenodeip/ip?" + params.Encode())
	resultS, err := http.Get(requestUrlS)
	if err != nil {
		logOnStderr(fmt.Errorf("get ip", err))
	}
	ipResp := new(AlllocateResult)
	err = json.NewDecoder(resultS.Body).Decode(&ipResp)
	if err != nil {
		logOnStderr(fmt.Errorf("get ip", err))
	}
	_, address, err := net.ParseCIDR(ipResp.Message)
	Result := &current.Result{}
	Result.IPs = []*current.IPConfig{
		{
			Version: "4",
			Address: net.IPNet(*address),
		},
	}
	logOnStderr(fmt.Errorf("got ip from galaxy", Result, err))
	return Result, err

}

func GetOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		logOnStderr(fmt.Errorf("get local ip", err))
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}

func GetIPFromAnnotation(kubeconfig string, args *skel.CmdArgs) (*current.Result, error) {
	var pod *corev1.Pod
	cniArgs, err := ParseCNIArgs(args.Args)
	if err != nil {
		return nil, err
	}
	PodNamespace, _ := cniArgs[K8S_POD_NAMESPACE]
	PodName, _ := cniArgs[K8S_POD_NAME]
	printOnce := false
	//config, err := rest.InClusterConfig()
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	logOnStderr(fmt.Errorf("get pods init configxxxxxxxxxxxxxxxxxxxxxx", err))
	logOnStderr(fmt.Errorf("get pods init configxxxxxxxxxxxxxxxxxxxxxx", err))
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("failed to get pod %s_%s: %v", PodName, PodNamespace, err)
	}
	if err != nil {
		return nil, err
	}
	extendedCNIArgs, err := parseExtendedCNIArgs(pod)
	var infos []IPInfo
	for k, v := range extendedCNIArgs {
		if k == "ipinfos" {
			if err := json.Unmarshal(v, &infos); err != nil {
				logOnStderr(fmt.Errorf("getpod2xxxxxxxxxxxxxxxxxxxxxxargs", err))
			}
		}
		logOnStderr(fmt.Errorf("getpod2xxxxxxxxxxxxxxxxxxxxxxargs", k, v))
	}
	if len(infos) == 0 {
		return nil, nil
	} else {
		newResult := IPInfoToResult(infos)
		return newResult, nil

	}
}

func logOnStderr(err error) {
	fmt.Fprintln(os.Stderr, "weave-cni:", err)
}

