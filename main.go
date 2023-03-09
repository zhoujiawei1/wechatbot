package main

import (
	// "bytes"
	"container/list"
	"context"
	// "encoding/json"
	// "errors"
	"flag"
	"github.com/eatmoreapple/openwechat"
	openai "github.com/sashabaranov/go-openai"
	// "io/ioutil"
	"log"
	"net/http"
	"strings"
	"sync"
)

var l sync.Mutex
var msgListMap = make(map[string]*list.List)
var apiKey string
var groupIdOnly string
var storagePath string
var client *openai.Client

func ImagesGenerations(msg string, role string, group string) ([]string, error) {
	req := openai.ImageRequest{
		Prompt: msg,
		N:      4,
		Size:   openai.CreateImageSize1024x1024,
		User:   role,
	}
	log.Printf("CreateImage begin: req=%v\n", req)
	client = openai.NewClient(apiKey)
	rsp, err := client.CreateImage(context.Background(), req)
	log.Printf("CreateImage end: err=%v, rsp=%v \n", err, rsp)
	if err != nil {
		return nil, err
	}
	reply := []string{}
	for _, v := range rsp.Data {
		reply = append(reply, v.URL)
	}
	return reply, nil
}

func ChatCompletions(msg string, role string, group string) ([]string, error) {
	message := openai.ChatCompletionMessage{
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
	messages := []openai.ChatCompletionMessage{}
	for e := msgListMap[group].Front(); e != nil; e = e.Next() {
		messages = append(messages, e.Value.(openai.ChatCompletionMessage))
	}
	l.Unlock()

	req := openai.ChatCompletionRequest{
		Model:       "gpt-3.5-turbo",
		Messages:    messages,
		Temperature: 0.2,
	}
	log.Printf("CreateChatCompletion begin: req=%v\n", req)
	client = openai.NewClient(apiKey)
	rsp, err := client.CreateChatCompletion(context.Background(), req)
	log.Printf("CreateChatCompletion end:err=%v, rsp=%v\n", err, rsp)
	if err != nil {
		return nil, err
	}
	reply := []string{}
	for _, choice := range rsp.Choices {
		reply = append(reply, choice.Message.Content)
	}
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
	requestText := strings.TrimSpace(strings.ReplaceAll(msg.Content, replaceText, ""))
	// log.Printf("replace : %s|%s|%s", msg.Content, replaceText, requestText)
	role := "user"
	if strings.HasPrefix(requestText, "[system]") == true {
		requestText = strings.TrimSpace(strings.ReplaceAll(requestText, "[system]", ""))
		role = "system"
	}
	images := false
	if strings.HasPrefix(requestText, "[images]") == true {
		requestText = strings.TrimSpace(strings.ReplaceAll(requestText, "[images]", ""))
		images = true
	}
	var replys []string
	var err error
	if images == true {
		replys, err = ImagesGenerations(requestText, role, msg.FromUserName)
	} else {
		replys, err = ChatCompletions(requestText, role, msg.FromUserName)
	}
	if err != nil {
		log.Printf("gtp request error: %v \n", err)
		msg.ReplyText("gtp request error. " + err.Error())
		return err
	}
	if len(replys) == 0 {
		msg.ReplyText("reply nothing.")
		return nil
	}

	// 回复
	for _, reply := range replys {
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
				return err
			}
		} else {
			reply = strings.TrimSpace(reply)
			reply = strings.Trim(reply, "\n")
			replyText := reply
			_, err = msg.ReplyText(replyText)
			if err != nil {
				log.Printf("response group error: %v \n", err)
				return err
			}
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

func HttpStart() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		len := r.ContentLength
		body := make([]byte, len)
		r.Body.Read(body)
		log.Printf("http body is: %s\n", string(body))
		w.Write([]byte("Hello, world!"))
	})
	log.Printf("%v", http.ListenAndServe("0.0.0.0:80", nil))
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
	// start http
	go HttpStart()
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
