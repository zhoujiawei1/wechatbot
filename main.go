package main

import (
	"bytes"
	"container/list"
	"encoding/json"
	"errors"
	"flag"
	"github.com/eatmoreapple/openwechat"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"sync"
)

type ChatGPTResponseBody struct {
	ID      string                   `json:"id"`
	Object  string                   `json:"object"`
	Created int                      `json:"created"`
	Model   string                   `json:"model"`
	Choices []map[string]interface{} `json:"choices"`
	Usage   map[string]interface{}   `json:"usage"`
	Error   map[string]interface{}   `json:"error"`
}

type ChoiceItem struct {
}

type ChatGPTMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatGPTRequestBody struct {
	Model       string           `json:"model"`
	Messages    []ChatGPTMessage `json:"messages"`
	Temperature float32          `json:"temperature"`
}

type ImageGenRequestBody struct {
	Prompt string `json:"prompt"`
	N      int    `json:"n"`
	Size   string `json:"size"`
}

type ImageGenResponseBody struct {
	Created int                      `json:"created"`
	Data    []map[string]interface{} `json:"data"`
	Error   map[string]interface{}   `json:"error"`
}

var l sync.Mutex
var msgListMap = make(map[string]*list.List)
var apiKey string
var groupIdOnly string
var storagePath string

func ImagesGenerations(msg string, role string, group string) (string, error) {
	requestBody := ImageGenRequestBody{
		Prompt: msg,
		N:      1,
		Size:   "1024x1024",
	}
	requestData, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}
	log.Printf("request gtp json string : %v", string(requestData))
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/images/generations", bytes.NewBuffer(requestData))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{}
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "", err
	}

	gptResponseBody := &ImageGenResponseBody{}
	log.Println(string(body))
	err = json.Unmarshal(body, gptResponseBody)
	if err != nil {
		return "", err
	}
	var reply string
	if _, ok := gptResponseBody.Error["message"]; ok {
		reply = gptResponseBody.Error["message"].(string)
		return reply, errors.New(reply)
	}
	if len(gptResponseBody.Data) > 0 {
		for _, v := range gptResponseBody.Data {
			if _, ok := v["url"]; ok {
				reply = v["url"].(string)
			}
			break
		}
	}

	return reply, nil
}

func ChatCompletions(msg string, role string, group string) (string, error) {
	message := ChatGPTMessage{
		Role:    role,
		Content: msg,
	}
	l.Lock()
	if _, ok := msgListMap[group]; !ok {
		msgListMap[group] = list.New()
	}
	log.Printf("msgListMap[group].len = %d", msgListMap[group].Len())
	if msgListMap[group].Len() == 10 {
		msgListMap[group].Remove(msgListMap[group].Front())
	}
	msgListMap[group].PushBack(message)
	messages := []ChatGPTMessage{}
	for e := msgListMap[group].Front(); e != nil; e = e.Next() {
		messages = append(messages, e.Value.(ChatGPTMessage))
	}
	l.Unlock()

	requestBody := ChatGPTRequestBody{
		Model:       "gpt-3.5-turbo",
		Messages:    messages,
		Temperature: 0.2,
	}
	requestData, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}
	log.Printf("request gtp json string : %v", string(requestData))
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewBuffer(requestData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{}
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "", err
	}

	gptResponseBody := &ChatGPTResponseBody{}
	log.Println(string(body))
	err = json.Unmarshal(body, gptResponseBody)
	if err != nil {
		return "", err
	}
	var reply string
	if _, ok := gptResponseBody.Error["message"]; ok {
		reply = gptResponseBody.Error["message"].(string)
		return reply, errors.New(reply)
	}
	if len(gptResponseBody.Choices) > 0 {
		for _, v := range gptResponseBody.Choices {
			if _, ok := v["message"]; ok {
				if _, ok := v["message"].(map[string]interface{})["content"]; ok {
					replyMsg := ChatGPTMessage{
						Role:    v["message"].(map[string]interface{})["role"].(string),
						Content: v["message"].(map[string]interface{})["content"].(string),
					}
					reply = replyMsg.Content
					l.Lock()
					if _, ok := msgListMap[group]; !ok {
						msgListMap[group] = list.New()
					}
					log.Printf("msgListMap[group].len = %d", msgListMap[group].Len())
					if msgListMap[group].Len() == 10 {
						msgListMap[group].Remove(msgListMap[group].Front())
					}
					msgListMap[group].PushBack(replyMsg)
					l.Unlock()
					break
				}
			}
		}
	}

	// log.Printf("gpt response text: %s \n", reply)
	return reply, nil
}

func ReplyText(msg *openwechat.Message) error {
	// 接收群消息
	// sender, err := msg.Sender()
	// group := openwechat.Group{sender}
	log.Printf("Received Sender %s Text Msg : %v", msg.FromUserName, msg.Content)
	if groupIdOnly != "" && msg.FromUserName != groupIdOnly {
		return nil
	}
	// 不是@的不处理
	if !msg.IsAt() {
		return nil
	}
	receiver, _ := msg.Receiver()
	// 替换掉@文本，然后向GPT发起请求
	replaceText := "@" + receiver.Self().NickName
	requestText := strings.ReplaceAll(msg.Content, replaceText, "")
	// log.Printf("replace : %s|%s|%s", msg.Content, replaceText, requestText)
	role := "user"
	if strings.HasPrefix(requestText, "[system]") == true {
		requestText = strings.ReplaceAll(requestText, "[system]", "")
		role = "system"
	}
	images := false
	if strings.HasPrefix(requestText, "[images]") == true {
		requestText = strings.ReplaceAll(requestText, "[images]", "")
		images = true
	}
	requestText = strings.TrimSpace(requestText)
	var reply string
	var err error
	if images == true {
		reply, err = ImagesGenerations(requestText, role, msg.FromUserName)
	} else {
		reply, err = ChatCompletions(requestText, role, msg.FromUserName)
	}
	if err != nil {
		log.Printf("gtp request error: %v \n", err)
		msg.ReplyText("gtp request error. " + err.Error())
		return err
	}
	if reply == "" {
		msg.ReplyText("reply nothing.")
		return nil
	}

	// 回复
	if err == nil && images == true {
		req, err := http.NewRequest("GET", reply, nil)
		if err != nil {
			return err
		}
		client := &http.Client{}
		rsp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer rsp.Body.Close()
		_, err = msg.ReplyImage(rsp.Body)
		if err != nil {
			log.Printf("response group error: %v \n", err)
		}
	} else {
		reply = strings.TrimSpace(reply)
		reply = strings.Trim(reply, "\n")
		replyText := reply
		_, err = msg.ReplyText(replyText)
		if err != nil {
			log.Printf("response group error: %v \n", err)
		}
	}
	return err
}

func Handler(msg *openwechat.Message) {
	log.Printf("hadler Received msg : %v", msg.Content)
	if msg.IsSendByGroup() {
		ReplyText(msg)
		return
	}
}

func main() {
	flag.StringVar(&apiKey, "apiKey", "", "")
	flag.StringVar(&groupIdOnly, "groupIdOnly", "", "")
	flag.StringVar(&storagePath, "storagePath", "storage.json", "")
	flag.Parse()
	if apiKey == "" {
		log.Printf("start error: need -apiKey\n")
		return
	}

	log.Printf("apiKey = %s, groupIdOnly = %s, storagePath = %s\n", apiKey, groupIdOnly, storagePath)
	//bot := openwechat.DefaultBot()
	bot := openwechat.DefaultBot(openwechat.Desktop) // 桌面模式，上面登录不上的可以尝试切换这种模式
	// 注册消息处理函数
	bot.MessageHandler = Handler
	// 注册登陆二维码回调
	bot.UUIDCallback = openwechat.PrintlnQrcodeUrl
	// 创建热存储容器对象
	reloadStorage := openwechat.NewJsonFileHotReloadStorage(storagePath)
	// 执行热登录
	err := bot.HotLogin(reloadStorage)
	if err != nil {
		if err = bot.Login(); err != nil {
			log.Printf("login error: %v \n", err)
			return
		}
	}
	// 阻塞主goroutine, 直到发生异常或者用户主动退出
	bot.Block()
}
