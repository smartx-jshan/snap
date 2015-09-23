package client

import (
	"bytes"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync/atomic"
	"time"

	log "github.com/Sirupsen/logrus"

	"github.com/intelsdi-x/pulse/control/plugin"
	"github.com/intelsdi-x/pulse/control/plugin/cpolicy"
	"github.com/intelsdi-x/pulse/control/plugin/encoding"
	"github.com/intelsdi-x/pulse/control/plugin/encrypter"
	"github.com/intelsdi-x/pulse/core"
	"github.com/intelsdi-x/pulse/core/ctypes"
)

var logger = log.WithField("_module", "client-httpjsonrpc")

type httpJSONRPCClient struct {
	url        string
	id         uint64
	timeout    time.Duration
	pluginType plugin.PluginType
	encrypter  *encrypter.Encrypter
	encoder    encoding.Encoder
}

// NewCollectorHttpJSONRPCClient returns CollectorHttpJSONRPCClient
func NewCollectorHttpJSONRPCClient(u string, timeout time.Duration, pub *rsa.PublicKey, secure bool) (PluginCollectorClient, error) {
	hjr := &httpJSONRPCClient{
		url:        u,
		timeout:    timeout,
		pluginType: plugin.CollectorPluginType,
		encoder:    encoding.NewJsonEncoder(),
	}
	if secure {
		key, err := encrypter.GenerateKey()
		if err != nil {
			return nil, err
		}
		e := encrypter.New(pub, nil)
		e.Key = key
		hjr.encoder.SetEncrypter(e)
		hjr.encrypter = e
	}
	return hjr, nil
}

func NewProcessorHttpJSONRPCClient(u string, timeout time.Duration, pub *rsa.PublicKey, secure bool) (PluginProcessorClient, error) {
	hjr := &httpJSONRPCClient{
		url:        u,
		timeout:    timeout,
		pluginType: plugin.ProcessorPluginType,
		encoder:    encoding.NewJsonEncoder(),
	}
	if secure {
		key, err := encrypter.GenerateKey()
		if err != nil {
			return nil, err
		}
		e := encrypter.New(pub, nil)
		e.Key = key
		hjr.encoder.SetEncrypter(e)
		hjr.encrypter = e
	}
	return hjr, nil
}

func NewPublisherHttpJSONRPCClient(u string, timeout time.Duration, pub *rsa.PublicKey, secure bool) (PluginPublisherClient, error) {
	hjr := &httpJSONRPCClient{
		url:        u,
		timeout:    timeout,
		pluginType: plugin.PublisherPluginType,
		encoder:    encoding.NewJsonEncoder(),
	}
	if secure {
		key, err := encrypter.GenerateKey()
		if err != nil {
			return nil, err
		}
		e := encrypter.New(pub, nil)
		e.Key = key
		hjr.encoder.SetEncrypter(e)
		hjr.encrypter = e
	}
	return hjr, nil
}

// Ping
func (h *httpJSONRPCClient) Ping() error {
	_, err := h.call("SessionState.Ping", []interface{}{})
	return err
}

func (h *httpJSONRPCClient) SetKey() error {
	key, err := h.encrypter.EncryptKey()
	if err != nil {
		return err
	}
	a := plugin.SetKeyArgs{Key: key}
	_, err = h.call("SessionState.SetKey", []interface{}{a})
	return err
}

// kill
func (h *httpJSONRPCClient) Kill(reason string) error {
	args := plugin.KillArgs{Reason: reason}
	out, err := h.encoder.Encode(args)
	if err != nil {
		return err
	}

	_, err = h.call("SessionState.Kill", []interface{}{out})
	return err
}

// CollectMetrics returns collected metrics
func (h *httpJSONRPCClient) CollectMetrics(mts []core.Metric) ([]core.Metric, error) {
	// Here we create two slices from the requested metric collection. One which
	// contains the metrics we retreived from the cache, and one from which we had
	// to use the plugin.

	// This is managed by walking through the complete list and hitting the cache for each item.
	// If the metric is found in the cache, we nil out that entry in the complete collection.
	// Then, we walk through the collection once more and create a new slice of metrics which
	// were not found in the cache.
	var fromCache []core.Metric
	for i, m := range mts {
		var metric core.Metric
		if metric = metricCache.get(core.JoinNamespace(m.Namespace())); metric != nil {
			fromCache = append(fromCache, metric)
			mts[i] = nil
		}
	}
	var fromPlugin []plugin.PluginMetricType
	for _, mt := range mts {
		if mt != nil {
			fromPlugin = append(fromPlugin, plugin.PluginMetricType{
				Namespace_: mt.Namespace(),
				Config_:    mt.Config(),
			})
		}
	}
	// We only need to send a request to the plugin if there are metrics which were not available in the cache.
	if len(fromPlugin) > 0 {
		args := &plugin.CollectMetricsArgs{PluginMetricTypes: fromPlugin}
		out, err := h.encoder.Encode(args)
		if err != nil {
			return nil, err
		}
		res, err := h.call("Collector.CollectMetrics", []interface{}{out})
		if err != nil {
			return nil, err
		}
		if len(res.Result) == 0 {
			err := errors.New("Invalid response: result is 0")
			logger.WithFields(log.Fields{
				"_block":           "CollectMetrics",
				"jsonrpc response": fmt.Sprintf("%+v", res),
			}).Error(err)
			return nil, err
		}
		var mtr plugin.CollectMetricsReply
		err = h.encoder.Decode(res.Result, &mtr)
		if err != nil {
			return nil, err
		}
		for _, m := range mtr.PluginMetrics {
			metricCache.put(core.JoinNamespace(m.Namespace()), m)
			fromCache = append(fromCache, m)
		}
	}
	return fromCache, nil
}

// GetMetricTypes returns metric types that can be collected
func (h *httpJSONRPCClient) GetMetricTypes() ([]core.Metric, error) {
	res, err := h.call("Collector.GetMetricTypes", []interface{}{})
	if err != nil {
		return nil, err
	}
	var mtr plugin.GetMetricTypesReply
	err = h.encoder.Decode(res.Result, &mtr)
	if err != nil {
		return nil, err
	}
	metrics := make([]core.Metric, len(mtr.PluginMetricTypes))
	for i, mt := range mtr.PluginMetricTypes {
		metrics[i] = mt
	}
	return metrics, nil
}

// GetConfigPolicy returns a config policy
func (h *httpJSONRPCClient) GetConfigPolicy() (*cpolicy.ConfigPolicy, error) {
	res, err := h.call("SessionState.GetConfigPolicy", []interface{}{})
	if err != nil {
		logger.WithFields(log.Fields{
			"_block": "GetConfigPolicy",
			"result": fmt.Sprintf("%+v", res),
			"error":  err,
		}).Error("error getting config policy")
		return nil, err
	}
	if len(res.Result) == 0 {
		return nil, errors.New(res.Error)
	}
	var cpr plugin.GetConfigPolicyReply
	err = h.encoder.Decode(res.Result, &cpr)
	if err != nil {
		return nil, err
	}
	return cpr.Policy, nil
}

func (h *httpJSONRPCClient) Publish(contentType string, content []byte, config map[string]ctypes.ConfigValue) error {
	args := plugin.PublishArgs{ContentType: contentType, Content: content, Config: config}
	out, err := h.encoder.Encode(args)
	if err != nil {
		return nil
	}
	_, err = h.call("Publisher.Publish", []interface{}{out})
	if err != nil {
		return err
	}
	return nil
}

func (h *httpJSONRPCClient) Process(contentType string, content []byte, config map[string]ctypes.ConfigValue) (string, []byte, error) {
	args := plugin.ProcessorArgs{ContentType: contentType, Content: content, Config: config}
	out, err := h.encoder.Encode(args)
	if err != nil {
		return "", nil, err
	}
	res, err := h.call("Processor.Process", []interface{}{out})
	if err != nil {
		return "", nil, err
	}
	processorReply := &plugin.ProcessorReply{}
	if err := h.encoder.Decode(res.Result, processorReply); err != nil {
		return "", nil, err
	}
	return processorReply.ContentType, processorReply.Content, nil
}

func (h *httpJSONRPCClient) GetType() string {
	return upcaseInitial(h.pluginType.String())
}

type jsonRpcResp struct {
	Id     int    `json:"id"`
	Result []byte `json:"result"`
	Error  string `json:"error"`
}

func (h *httpJSONRPCClient) call(method string, args []interface{}) (*jsonRpcResp, error) {
	data, err := json.Marshal(map[string]interface{}{
		"method": method,
		"id":     h.id,
		"params": args,
	})
	if err != nil {
		logger.WithFields(log.Fields{
			"_block": "call",
			"url":    h.url,
			"args":   fmt.Sprintf("%+v", args),
			"method": method,
			"id":     h.id,
			"error":  err,
		}).Error("error encoding request to json")
		return nil, err
	}
	client := http.Client{Timeout: h.timeout}
	resp, err := client.Post(h.url, "application/json", bytes.NewReader(data))
	if err != nil {
		logger.WithFields(log.Fields{
			"_block":  "call",
			"url":     h.url,
			"request": string(data),
			"error":   err,
		}).Error("error posting request to plugin")
		return nil, err
	}
	defer resp.Body.Close()
	result := &jsonRpcResp{}
	if err = json.NewDecoder(resp.Body).Decode(result); err != nil {
		bs, _ := ioutil.ReadAll(resp.Body)
		logger.WithFields(log.Fields{
			"_block":      "call",
			"url":         h.url,
			"request":     string(data),
			"status code": resp.StatusCode,
			"response":    string(bs),
			"error":       err,
		}).Error("error decoding result")
		return nil, err
	}
	atomic.AddUint64(&h.id, 1)
	return result, nil
}
