package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/etcd/clientv3"
	"github.com/mattbaird/jsonpatch"
)

var loadConfigUrlStr = "http://localhost:9999/api/v1/_operations/loadConfig"
var updateUrlStr = "http://localhost:9999/updateConfig"

var vsConfigJson = "./DAY1_vs_Refactor.json"
var vsConfigJsonUpdate = "./DAY1_vs_Refactor_update.json"

var vsConfig2Json = "./gnb_vs_config.json"
var vsConfig2JsonUpdate = "./gnb_vs_config_update.json"

var threegppConfigJson = "./DAY1_3GPP_Refactor_0727.json"

var vsConfigFile = "gnb_vs_config.json"

var (
	Namespace        string
	MicroserviceName string
	AppVersion       string
	DayOneConfig     string
)

var configChangePath string
var dayOneConfigPath string

const (
	etcdHostAddr    = "127.0.0.1:2479"
	cimMtcilAppPort = ":9999"
)

var etcdClient *clientv3.Client
var etcdPutCount = 0
var testPatch string

var updateFileName string
var updateFilePath string

func init() {
	MicroserviceName = os.Getenv("MICROSERVICE_NAME")
	Namespace = os.Getenv("K8S_NAMESPACE")
	AppVersion = os.Getenv("APPVERSION")
	updateFilePath, updateFileName = filepath.Split(vsConfigFile)
}
func createEtcdClient() {
	var err error
	etcdClient, err = clientv3.New(clientv3.Config{
		Endpoints:   []string{etcdHostAddr},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		fmt.Println("create ETCD client error")
	}
}

func testPatchUpdate(ctx context.Context) {

	vsConfig, err := ioutil.ReadFile(vsConfig2Json)
	if err != nil {
		fmt.Println("read file", err)
		return
	}
	vsConfigUpdate, err := ioutil.ReadFile(vsConfig2JsonUpdate)
	if err != nil {
		fmt.Println("read update file", err)
		return
	}
	patch, e := jsonpatch.CreatePatch([]byte(vsConfig), []byte(vsConfigUpdate))
	if e != nil {
		fmt.Printf("Error creating JSON patch:%v", e)
		return
	}
	fmt.Println("The patch: ")
	fmt.Println(patch)

	var builder strings.Builder
	builder.WriteString("[")
	for i, operation := range patch {
		fmt.Println(operation.Json())
		testPatch = operation.Json()
		builder.WriteString(testPatch)
		if i != len(patch)-1 {
			builder.WriteString(",")
		}
	}
	builder.WriteString("]")
	sendBuf := builder.String()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Main ctx done")
			return
		default:
			{
				subStr := "/" + AppVersion + "/CUUP1.0/" + updateFileName + "/" + strconv.Itoa(etcdPutCount) + "/"
				etcdPutCount++
				kv := clientv3.NewKV(etcdClient)
				ctx, cancleFunc := context.WithTimeout(context.TODO(), 5*time.Second)
				putResp, err := kv.Put(ctx, configChangePath+subStr, sendBuf, clientv3.WithPrevKV())
				if err != nil {
					fmt.Println(err)
				} else {
					fmt.Println("Revision:", putResp.Header.Revision)
					if putResp.PrevKv != nil {
						fmt.Println("key:", string(putResp.PrevKv.Key))
						fmt.Println("Value:", string(putResp.PrevKv.Value))
						fmt.Println("Version:", string(putResp.PrevKv.Version))
					}
				}
				cancleFunc()
				fmt.Printf("PutResponse: %v, err: %v", putResp, err)
				time.Sleep(20 * time.Second)

				ctx, cancleFunc = context.WithTimeout(context.TODO(), 5*time.Second)
				getResp, err := kv.Get(ctx, configChangePath, clientv3.WithPrefix())
				if err != nil {
					panic(err)
				}
				cancleFunc()
				fmt.Println("%v", getResp.Kvs)
			}
		}
	}
}

//
func WatchConfigChange(ctx context.Context, etcd *clientv3.Client, etcdWatchKey, appPort string) {
	//watcher = clientv3.NewWatcher(etcd)
	fmt.Println("CIM start watch etcd")
	defer etcd.Close()
	watchChan := etcd.Watch(ctx, etcdWatchKey, clientv3.WithPrefix())
	if watchChan == nil {
		fmt.Println("watch channel is nill")
		return
	}

	for watchResp := range watchChan {
		select {
		case <-ctx.Done():
			{
				fmt.Println("Watch config Done")
				return
			}
		default:
			{
				go func(resp clientv3.WatchResponse) {
					for _, event := range resp.Events {
						handleEtcdUpdate(event)
					}
				}(watchResp)
			}
		}
	}
}

func watchDayOneConfig(ctx context.Context, etcd *clientv3.Client, etcdWatchKey, appPort string) {
	DayOneConfig = "config/" + Namespace + "/" + MicroserviceName
	fmt.Println("CIM start watch etcd")
	defer etcd.Close()
	watchChan := etcd.Watch(ctx, etcdWatchKey, clientv3.WithPrefix())
	if watchChan == nil {
		fmt.Println("watch channel is nill")
		return
	}

	for watchResp := range watchChan {
		select {
		case <-ctx.Done():
			{
				fmt.Println("Watch config Done")
				return
			}
		default:
			{
				go func(resp clientv3.WatchResponse) {
					for _, event := range resp.Events {
						handleEtcdUpdate(event)
					}
				}(watchResp)
			}
		}
	}
}
func startEtcdClient(ctx context.Context) {
	fmt.Println("Start etcd client")
	fmt.Println(configChangePath)
	fmt.Println(dayOneConfigPath)

	createEtcdClient()
	go testPatchUpdate(ctx)
	go WatchConfigChange(ctx, etcdClient, configChangePath, cimMtcilAppPort)
	watchDayOneConfig(ctx, etcdClient, dayOneConfigPath, cimMtcilAppPort)
}

func handleAppConfigUpdate(event *clientv3.Event, keys []string, eventType string) {
	var data = map[string]string{
		"change-set-key": string(event.Kv.Key),
		"data-key":       keys[5],
		"config-patch":   string(event.Kv.Value),
		"revision":       keys[6],
	}
	jsonValue, err := json.Marshal(data)
	if err != nil {
		fmt.Println("Error while marshalling the data", err)
		return
	}
	resp, err := http.Post(updateUrlStr, "application/json", bytes.NewReader(jsonValue))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(resp)
}

func handleEtcdUpdate(event *clientv3.Event) {
	fmt.Println("Event received: ", event.Type, "executed on", string(event.Kv.Key[:]), "with value", string(event.Kv.Value[:]))
	eventType := fmt.Sprintf("%s", event.Type)
	keys := strings.Split(string(event.Kv.Key), "/")
	if MicroserviceName == keys[2] && AppVersion == keys[3] && eventType == "PUT" {
		handleAppConfigUpdate(event, keys, eventType)
	}
}

func testJsonPatch() {
	var simpleA = `{"a":100, "b":200, "c":"hello"}`
	var simpleB = `{"a":100, "b":200, "c":"goodbye"}`
	patch, e := jsonpatch.CreatePatch([]byte(simpleA), []byte(simpleB))
	if e != nil {
		fmt.Printf("Error creating JSON patch:%v", e)
		return
	}
	for _, operation := range patch {
		fmt.Print("%s\n", operation.Json())
	}
}

func main() {

	//define a root context
	parent := context.Background()

	//create a withCancel
	ctx, cancel := context.WithCancel(parent)

	defer cancel()
	defer func() {
		time.Sleep(time.Second * 5)
	}()

	//configChangePath = "change-set/" + Namespace + "/" + MicroserviceName + "/" + AppVersion + "/CUUP1.0/"
	configChangePath = "change-set/" + Namespace + "/" + MicroserviceName
	dayOneConfigPath = "config/" + Namespace + "/" + MicroserviceName

	//send the day1 config
	vsConfig, err := ioutil.ReadFile(vsConfigJson)
	if err != nil {
		fmt.Println("readfile", err)
		return
	}

	resp, err := http.Post(loadConfigUrlStr, "application/json", bytes.NewReader(vsConfig))
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(resp, err)
	threeGppConfig, err := ioutil.ReadFile(threegppConfigJson)
	if err != nil {
		fmt.Println("readfile", err)
		return
	}
	resp, err = http.Post(loadConfigUrlStr, "application/json", bytes.NewReader(threeGppConfig))
	if err != nil {
		fmt.Println(err)
		return
	}

	go startEtcdClient(ctx)
	testJsonPatch()
	for {
		time.Sleep(time.Second * 10)
	}

	//cancel()

}
