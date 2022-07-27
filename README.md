# mini-websocket
websocket framework
### go version
> go 1.17+
### function 
- [x] 序列化和反序列化帧
- [x] 握手与挥手
- [x] 良好的封装
- [x] 心跳api 
- [x] 数据分片传输
- [x] 跨域处理
- [ ] 压缩

### start
```shell
go get -u github.com/cold-bin/mini-websocket
```
### 使用示例

```go
// @author cold bin
// @date 2022/7/25

package mini_websocket

import (
	"log"
	"net/http"

	websocket "github.com/cold-bin/mini-websocket"
)

func main() {
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		wsConn, err := websocket.DefaultUpGrader.UpGrade(c.Req, c.Writer)
		if err != nil {
			log.Println(err)
			return
		}
		wsConn.SendMessage("欢迎使用本websocket连接") //当前为server端，不会做掩码处理

		for {
			_, bytes, err := wsConn.ReadMessage()
			if err != nil {
				log.Println(err)
				return
			}

			wsConn.SendMessage(string(bytes))
		}
	})

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Println(err)
		return
	}
}
```