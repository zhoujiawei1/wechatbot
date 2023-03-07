# wechatbot

1. go mod tidy

2. go build main.go

3. pkill main

4. nohup ./main -apiKey ${apiKey} -storagePath storage.json >> main.log 2>&1 &
