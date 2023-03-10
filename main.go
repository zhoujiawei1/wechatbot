package main

import (
	// "bytes"
	"container/list"
	"context"
	// "encoding/json"
	// "errors"
	"flag"
	"github.com/dobyte/tencent-im"
	"github.com/dobyte/tencent-im/callback"
	"github.com/dobyte/tencent-im/group"
	"github.com/dobyte/tencent-im/private"
	"github.com/eatmoreapple/openwechat"
	openai "github.com/sashabaranov/go-openai"
	tencentyun "github.com/tencentyun/tls-sig-api-v2-golang/tencentyun"
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
var timAppId int
var timSecretKey string
var tim im.IM

func GetTimUserSign(user string, expire int32) (string, error) {
	sig, err := tencentyun.GenUserSig(
		timAppId, timSecretKey,
		user,
		int(expire),
	)
	if err != nil {
		log.Printf("GenUserSig failed. err=%v", err)
		return "", err
	}
	return sig, nil
}

func ImagesGenerations(msg string, role string) ([]string, error) {
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

func AddHistory(hkey string, msg openai.ChatCompletionMessage) {
	l.Lock()
	if _, ok := msgListMap[hkey]; !ok {
		msgListMap[hkey] = list.New()
	}
	log.Printf("msgListMap[hkey].len = %d", msgListMap[hkey].Len())
	if msgListMap[hkey].Len() == 10 {
		msgListMap[hkey].Remove(msgListMap[hkey].Front())

	}
	msgListMap[hkey].PushBack(msg)
	l.Unlock()
}

func GetHistory(hkey string) []openai.ChatCompletionMessage {
	l.Lock()
	messages := []openai.ChatCompletionMessage{}
	for e := msgListMap[hkey].Front(); e != nil; e = e.Next() {
		messages = append(messages, e.Value.(openai.ChatCompletionMessage))
	}
	l.Unlock()
	return messages
}

func ChatCompletions(msg string, role string, hkey string) ([]string, error) {
	message := openai.ChatCompletionMessage{
		Role:    role,
		Content: msg,
	}

	AddHistory(hkey, message)
	messages := GetHistory(hkey)

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
		AddHistory(hkey, choice.Message)
	}
	return reply, nil
}

func ReplyText(msg *openwechat.Message) error {
	// 接收群消息
	log.Printf("Received Sender %s Text Msg : %v", msg.FromUserName, msg.Content)
	if groupIdOnly != "" && msg.FromUserName != groupIdOnly {
		return nil
	}
	if !msg.IsAt() {
		return nil
	}
	receiver, _ := msg.Receiver()
	replaceText := "@" + receiver.Self().NickName
	requestText := strings.TrimSpace(strings.ReplaceAll(msg.Content, replaceText, ""))
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
		replys, err = ImagesGenerations(requestText, role)
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

func ImReplyImageToGroup(groupId string, msg string) error {
	return nil
}

func ImReplyTextToGroup(groupId string, msg string) error {
	message := group.NewMessage()
	message.SetForbidBeforeSendMsgCallback()
	message.SetForbidAfterSendMsgCallback()
	message.SetSender("admin")
	message.SetContent(private.MsgTextContent{
		Text: msg,
	})
	ret, err := tim.Group().SendMessage(groupId, message)
	if err != nil {
		log.Printf("im send message group error: %v \n", err)
		return err
	}
	log.Printf("im send message group ret: %v \n", ret)
	return nil
}

func AsyncReplyToGroup(groupId string, msg string) error {
	requestText := strings.TrimSpace(strings.ReplaceAll(msg, "@admin", ""))
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
		replys, err = ImagesGenerations(requestText, role)
	} else {
		replys, err = ChatCompletions(requestText, role, groupId)
	}
	if len(replys) == 0 {
		return nil
	}
	if err != nil {
		ImReplyTextToGroup(groupId, err.Error())
		return nil
	}

	// 回复
	for _, reply := range replys {
		if images == true {
			ImReplyImageToGroup(groupId, reply)
		} else {
			ImReplyTextToGroup(groupId, reply)
		}
	}
	return nil
}

func ImReplyTextToUser(sender string, userId string, msg string) error {
	message := private.NewMessage()
	message.SetSender(sender)
	message.SetReceivers(userId)
	message.SetForbidBeforeSendMsgCallback()
	message.SetForbidAfterSendMsgCallback()
	message.SetContent(private.MsgTextContent{
		Text: msg,
	})
	ret, err := tim.Private().SendMessage(message)
	if err != nil {
		log.Printf("im send message group error: %v \n", err)
		return err
	}
	log.Printf("im send message group ret: %v \n", ret)
	return nil
}

func ImReplyImageToUser(sender string, userId string, msg string) error {
	return nil
}

func AsyncReplyToUser(sender string, userId string, msg string) error {
	requestText := msg
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
		replys, err = ImagesGenerations(requestText, role)
	} else {
		replys, err = ChatCompletions(requestText, role, userId)
	}
	if len(replys) == 0 {
		return nil
	}
	if err != nil {
		ImReplyTextToUser(sender,userId, err.Error())
		return nil
	}

	// 回复
	for _, reply := range replys {
		if images == true {
			ImReplyImageToUser(sender, userId, reply)
		} else {
			ImReplyTextToUser(sender, userId, reply)
		}
	}
	return nil
}

func Handler(msg *openwechat.Message) {
	log.Printf("hadler Received msg : %v", msg.Content)
	if msg.IsSendByGroup() {
		ReplyText(msg)
		return
	}
}

func HttpStart() {
	tim = im.NewIM(&im.Options{
		AppId:     timAppId,
		AppSecret: timSecretKey,
		UserId:    "admin",
	})
	tim.Callback()
	tim.Callback().Register(callback.EventAfterPrivateMessageSend, func(ack callback.Ack, data interface{}) {
		// log.Printf("%v", data.(callback.AfterPrivateMessageSend))
		sender := data.(*callback.AfterPrivateMessageSend).FromUserId
		userId := data.(*callback.AfterPrivateMessageSend).ToUserId
		for _, msg := range data.(*callback.AfterPrivateMessageSend).MsgBody {
			if msg.MsgType == "TIMTextElem" && userId == "admin" {
				text := msg.MsgContent.(map[string]interface{})["Text"].(string)
				go AsyncReplyToUser(sender, userId, text)
			}
		}
		_ = ack.AckSuccess(0)
	})
	tim.Callback().Register(callback.EventAfterGroupMessageSend, func(ack callback.Ack, data interface{}) {
		groupId := data.(*callback.AfterGroupMessageSend).GroupId
		for _, msg := range data.(*callback.AfterGroupMessageSend).MsgBody {
			if msg.MsgType == "TIMTextElem" {
				text := msg.MsgContent.(map[string]interface{})["Text"].(string)
				if strings.HasPrefix(text, "@admin") == true {
					go AsyncReplyToGroup(groupId, text)
				}
			}
		}
		_ = ack.AckSuccess(0)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		tim.Callback().Listen(w, r)
	})
	log.Printf("%v", http.ListenAndServe("0.0.0.0:80", nil))
}

func main() {
	flag.StringVar(&apiKey, "apiKey", "", "")
	flag.StringVar(&groupIdOnly, "groupIdOnly", "", "")
	flag.StringVar(&storagePath, "storagePath", "storage.json", "")
	flag.StringVar(&timSecretKey, "timSecretKey", "", "")
	flag.IntVar(&timAppId, "timAppId", 0, "")
	flag.Parse()
	if apiKey == "" {
		log.Printf("start error: need -apiKey\n")
		return
	}
	log.Printf("apiKey = %s, groupIdOnly = %s, storagePath = %s, timAppId = %d, timSecretKey = %s\n", apiKey, groupIdOnly, storagePath, timAppId, timSecretKey)
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
