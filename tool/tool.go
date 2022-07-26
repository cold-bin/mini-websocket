// @author cold bin
// @date 2022/7/23

package tool

import (
	"crypto/sha1"
	"encoding/base64"
	"net/http"
)

const GUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11" //magic string

// IsWsHeader 检查header里的键值对是否是满足websocket标准
func IsWsHeader(header http.Header, expectK, expectV string) bool {
	//标准化头字段
	expectK = http.CanonicalHeaderKey(expectK)
	for _, v := range header[expectK] {
		if v == expectV {
			return true
		}
	}
	return false
}

func IsSWK(str string) bool {
	return len(str) == 24
}

// EncodeSWK 将客户端的Sec-WebSocket-Key和 GUID
// 编码成SHA-1哈希值，并返回哈希的base64编码
func EncodeSWK(swk string) string {
	o := sha1.New()
	o.Write([]byte(swk + GUID))
	return base64.StdEncoding.EncodeToString(o.Sum(nil))
}
