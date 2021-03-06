package collector

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bitly/go-simplejson"
	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
)

// 这三个常量用于给每个 Metrics 名字添加前缀
const (
	name      = "gdas_exporter"
	Namespace = "gdas"
	//Subsystem(s).
)

// Name 用于给前端页面显示 const 常量中定义的内容
func Name() string {
	return name
}

// GetToken 获取 Gdas 认证所需 Token
func GetToken(opts *GdasOpts) (token string, err error) {
	// 设置 json 格式的 request body
	jsonReqBody := []byte("{\"userName\":\"" + opts.Username + "\",\"passWord\":\"" + opts.password + "\"}")
	// 设置 URL
	url := fmt.Sprintf("%v/v1/login", opts.URL)
	// 设置 Request 信息
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonReqBody))
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Referer", fmt.Sprintf("%v/gdas", opts.URL))
	req.Header.Add("stime", fmt.Sprintf("%v", time.Now().UnixNano()/1e6))
	// 忽略 TLS 的证书验证
	ts := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	// 发送 Request 并获取 Response
	resp, err := (&http.Client{Transport: ts}).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		respBody, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("GetToken Error: %v\nReason:%v", resp.StatusCode, string(respBody))
	}
	defer resp.Body.Close()

	// 处理 Response Body,并获取 Token
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}
	jsonRespBody, err := simplejson.NewJson(respBody)
	if err != nil {
		return
	}
	logrus.Debugf("Get Token Status:\nResponseStatusCode：%v\nResponseBody：%v\n", resp.StatusCode, string(respBody))
	token, err = jsonRespBody.Get("token").String()
	if err != nil {
		return "", fmt.Errorf("GetToken Error：%v", err)
	}
	logrus.Debugf("Get Token Successed!Token is:%v ", token)
	return
}

// ######## 从此处开始到文件结尾，都是关于配置连接 Gdas 的代码 ########

// GdasClient 连接 Gdas 所需信息
type GdasClient struct {
	Client *http.Client
	Token  string
	Opts   *GdasOpts
}

// NewGdasClient 实例化 Gdas 客户端
func NewGdasClient(opts *GdasOpts) *GdasClient {
	uri := opts.URL
	if !strings.Contains(uri, "://") {
		uri = "http://" + uri
	}
	u, err := url.Parse(uri)
	if err != nil {
		panic(fmt.Sprintf("invalid Gdas URL: %s", err))
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		panic(fmt.Sprintf("invalid Gdas URL: %s", uri))
	}

	// ######## 配置 http.Client 的信息 ########
	rootCAs, err := x509.SystemCertPool()
	if err != nil {
		panic(err)
	}
	// 初始化 TLS 相关配置信息
	tlsClientConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAs,
	}
	// 可以通过命令行选项配置 TLS 的 InsecureSkipVerify
	// 这个配置决定是否跳过 https 协议的验证过程，就是 curl 加不加 -k 选项。默认跳过
	if opts.Insecure {
		tlsClientConfig.InsecureSkipVerify = true
	}
	transport := &http.Transport{
		TLSClientConfig: tlsClientConfig,
	}
	// ######## 配置 http.Client 的信息结束 ########

	//
	token, err := GetToken(opts)
	if err != nil {
		panic(err)
	}
	return &GdasClient{
		Opts:  opts,
		Token: token,
		Client: &http.Client{
			Timeout:   opts.Timeout,
			Transport: transport,
		},
	}
}

// Request 建立与 Gdas 的连接，并返回 Response Body
func (g *GdasClient) Request(method string, endpoint string, reqBody io.Reader) (body []byte, err error) {
	// 根据认证信息及 endpoint 参数，创建与 Gdas 的连接，并返回 Body 给每个 Metric 采集器
	url := g.Opts.URL + endpoint
	logrus.Debugf("request url %s", url)

	// 生成签名所需数据
	// 毫秒时间戳
	stime := strconv.FormatInt(time.Now().UnixNano()/1e6, 10)
	// 随机数
	randString := rand.Intn(100000)
	// 随机数倒序
	stringRand := []rune(strconv.Itoa(randString))
	for from, to := 0, len(stringRand)-1; from < to; from, to = from+1, to-1 {
		stringRand[from], stringRand[to] = stringRand[to], stringRand[from]
	}
	// 签名
	signature := stime + strconv.Itoa(randString) + g.Token + string(stringRand)
	h := sha256.New()
	h.Write([]byte(signature))                     // 需要加密的字符串为
	signatureSha := hex.EncodeToString(h.Sum(nil)) // 输出加密结果

	// 创建一个新的 Request
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(g.Opts.Username, g.Opts.password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("token", g.Token)
	req.Header.Set("stime", fmt.Sprintf("%v", time.Now().UnixNano()/1e6))
	req.Header.Set("nonce", strconv.Itoa(randString))
	req.Header.Set("signature", signatureSha)
	req.Header.Set("Referer", fmt.Sprintf("%v/gdas", g.Opts.URL))

	// 根据新建立的 Request，发起请求，并获取 Response
	resp, err := g.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("error handling request for %s http-statuscode: %s", endpoint, resp.Status)
	}

	// 处理 Response Body
	body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	logrus.Debugf("Response Status:\nResponseStatusCode：%v\nResponseBody：%v\n", resp.StatusCode, string(body))
	return body, nil
}

// Ping 在 Scraper 接口的实现方法 scrape() 中调用。
// 让 Exporter 每次获取数据时，都检验一下目标设备通信是否正常
func (g *GdasClient) Ping() (b bool, err error) {
	logrus.Debugf("每次从 Gdas 并发抓取指标之前，先检查一下目标状态")
	// // 判断是否有 TOKEN
	if g.Token == "" {
		logrus.Debugf("Token 为空，开始尝试获取 Token")
		g.Token, err = GetToken(g.Opts)
		if err == nil {
			return true, nil
		}
		return false, err
	}
	logrus.Debugf("Xsky Token 为: %s", g.Token)

	// 判断 TOKEN 是否可用
	url := g.Opts.URL + "/v1/nodeList"
	logrus.Debugf("Ping Request url %s", url)

	// 生成 Request Header 中
	// 毫秒时间戳
	stime := strconv.FormatInt(time.Now().UnixNano()/1e6, 10)
	// 随机数
	randString := rand.Intn(100000)
	// 随机数倒序
	stringRand := []rune(strconv.Itoa(randString))
	for from, to := 0, len(stringRand)-1; from < to; from, to = from+1, to-1 {
		stringRand[from], stringRand[to] = stringRand[to], stringRand[from]
	}
	// 签名
	signature := stime + strconv.Itoa(randString) + g.Token + string(stringRand)
	h := sha256.New()
	h.Write([]byte(signature))                     // 需要加密的字符串为
	signatureSha := hex.EncodeToString(h.Sum(nil)) // 输出加密结果

	// 创建一个新的 Request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("token", g.Token)
	req.Header.Set("stime", fmt.Sprintf("%v", time.Now().UnixNano()/1e6))
	req.Header.Set("nonce", strconv.Itoa(randString))
	req.Header.Set("signature", signatureSha)
	req.Header.Set("Referer", fmt.Sprintf("%v/gdas", g.Opts.URL))

	// 根据新建立的 Request，发起请求，并获取 Response
	resp, err := g.Client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		return true, nil
	case resp.StatusCode == http.StatusUnauthorized:
		fmt.Printf("认证检查失败，状态码为：%v,尝试重新获取 Token\n", resp.Status)
		if g.Token, err = GetToken(g.Opts); err != nil {
			return false, err
		}
		return true, nil
	default:
		fmt.Println("检查失败，状态码为：", resp.Status)
		g.Token, err = GetToken(g.Opts)
		if err != nil {
			return false, fmt.Errorf("error handling request, http-statuscode: %s,http-ResponseBody：%s", resp.Status, resp.Body)
		}
		return true, nil
	}
}

// GdasOpts 登录 Gdas 所需属性
type GdasOpts struct {
	URL      string
	Username string
	password string
	// 这俩是关于 http.Client 的选项
	Timeout  time.Duration
	Insecure bool
}

// AddFlag use after set Opts
func (o *GdasOpts) AddFlag() {
	pflag.StringVar(&o.URL, "gdas-server", "https://172.38.30.193:8003", "HTTP API address of a Gdas server or agent. (prefix with https:// to connect over HTTPS)")
	pflag.StringVar(&o.Username, "gdas-user", "system", "gdas username")
	pflag.StringVar(&o.password, "gdas-pass", "", "gdas password")
	pflag.DurationVar(&o.Timeout, "time-out", time.Millisecond*1600, "Timeout on HTTP requests to the Gads API.")
	pflag.BoolVar(&o.Insecure, "insecure", true, "Disable TLS host verification.")
}
