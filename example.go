// @author cold bin
// @date 2022/7/23

package mini_websocket

import (
	"log"
	"net/http"
)

func main() {
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		wsConn, err := DefaultUpGrader.UpGrade(r, w)
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
