// @description wechat 是腾讯微信公众平台 api 的 golang 语言封装
// @link        https://github.com/chanxuehong/wechat for the canonical source repository
// @license     https://github.com/chanxuehong/wechat/blob/master/LICENSE
// @authors     chanxuehong(chanxuehong@gmail.com)

// +build !wechatdebug

package suite

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/chanxuehong/wechat/util"
	"github.com/chanxuehong/wechat/corp"
)

// 微信服务器请求 http body
type CorpRequestHttpBody struct {
	XMLName      struct {} `xml:"xml" json:"-"`

	CorpId       string `xml:"ToUserName"`
	AgentId      int64  `xml:"AgentID"`
	EncryptedMsg string `xml:"Encrypt"`
}

// ServeHTTP 处理 http 消息请求
//  NOTE: 调用者保证所有参数有效
func CorpServeHTTP(w http.ResponseWriter, r *http.Request, queryValues url.Values, srv corp.AgentServer, errHandler corp.ErrorHandler) {
	switch r.Method {
	case "POST": // 消息处理
		msgSignature1 := queryValues.Get("msg_signature")
		if msgSignature1 == "" {
			errHandler.ServeError(w, r, errors.New("msg_signature is empty"))
			return
		}
		if len(msgSignature1) != 40 { // sha1
			err := fmt.Errorf("the length of msg_signature mismatch, have: %d, want: 40", len(msgSignature1))
			errHandler.ServeError(w, r, err)
			return
		}

		timestampStr := queryValues.Get("timestamp")
		if timestampStr == "" {
			errHandler.ServeError(w, r, errors.New("timestamp is empty"))
			return
		}

		timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
		if err != nil {
			err = errors.New("can not parse timestamp to int64: " + timestampStr)
			errHandler.ServeError(w, r, err)
			return
		}

		nonce := queryValues.Get("nonce")
		if nonce == "" {
			errHandler.ServeError(w, r, errors.New("nonce is empty"))
			return
		}

		// 解析 CorpRequestHttpBody
		var requestHttpBody CorpRequestHttpBody
		if err := xml.NewDecoder(r.Body).Decode(&requestHttpBody); err != nil {
			errHandler.ServeError(w, r, err)
			return
		}

		corpId := srv.CorpId()

		haveCorpId := requestHttpBody.CorpId
		if len(haveCorpId) != len(corpId) {
			err = fmt.Errorf("the RequestHttpBody's ToUserName mismatch, have: %s, want: %s", haveCorpId, corpId)
			errHandler.ServeError(w, r, err)
			return
		}
		if subtle.ConstantTimeCompare([]byte(haveCorpId), []byte(corpId)) != 1 {
			err = fmt.Errorf("the RequestHttpBody's ToUserName mismatch, have: %s, want: %s", haveCorpId, corpId)
			errHandler.ServeError(w, r, err)
			return
		}


		haveAgentId := requestHttpBody.AgentId


		agentToken := srv.Token()

		// 验证签名
		msgSignature2 := util.MsgSign(agentToken, timestampStr, nonce, requestHttpBody.EncryptedMsg)
		if subtle.ConstantTimeCompare([]byte(msgSignature1), []byte(msgSignature2)) != 1 {
			err = fmt.Errorf("check msg_signature failed, input: %s, local: %s", msgSignature1, msgSignature2)
			errHandler.ServeError(w, r, err)
			return
		}

		// 解密
		encryptedMsgBytes, err := base64.StdEncoding.DecodeString(requestHttpBody.EncryptedMsg)
		if err != nil {
			errHandler.ServeError(w, r, err)
			return
		}

		aesKey := srv.CurrentAESKey()
		random, rawMsgXML, err := util.AESDecryptMsg(encryptedMsgBytes, corpId, aesKey)
		if err != nil {
			// 尝试用上一次的 AESKey 来解密
			lastAESKey, isLastAESKeyValid := srv.LastAESKey()
			if !isLastAESKeyValid {
				errHandler.ServeError(w, r, err)
				return
			}

			aesKey = lastAESKey // NOTE

			random, rawMsgXML, err = util.AESDecryptMsg(encryptedMsgBytes, corpId, aesKey)
			if err != nil {
				errHandler.ServeError(w, r, err)
				return
			}
		}

		// 解密成功, 解析 MixedMessage
		var mixedMsg corp.MixedMessage
		if err = xml.Unmarshal(rawMsgXML, &mixedMsg); err != nil {
			errHandler.ServeError(w, r, err)
			return
		}

		// 安全考虑再次验证
		if haveCorpId != mixedMsg.ToUserName {
			err = fmt.Errorf("the RequestHttpBody's ToUserName(==%s) mismatch the MixedMessage's ToUserName(==%s)", haveCorpId, mixedMsg.ToUserName)
			errHandler.ServeError(w, r, err)
			return
		}



		// 成功, 交给 MessageHandler
		req := &corp.Request{
			HttpRequest: r,

			QueryValues:  queryValues,
			MsgSignature: msgSignature1,
			Timestamp:    timestamp,
			Nonce:        nonce,

			RawMsgXML: rawMsgXML,
			MixedMsg:  &mixedMsg,

			AESKey: aesKey,
			Random: random,

			CorpId:     haveCorpId,
			AgentId:    haveAgentId,
			AgentToken: agentToken,
		}
		srv.MessageHandler().ServeMessage(w, req)

	case "GET": // 首次验证
		msgSignature1 := queryValues.Get("msg_signature")
		if msgSignature1 == "" {
			errHandler.ServeError(w, r, errors.New("msg_signature is empty"))
			return
		}
		if len(msgSignature1) != 40 { // sha1
			err := fmt.Errorf("the length of msg_signature mismatch, have: %d, want: 40", len(msgSignature1))
			errHandler.ServeError(w, r, err)
			return
		}

		timestamp := queryValues.Get("timestamp")
		if timestamp == "" {
			errHandler.ServeError(w, r, errors.New("timestamp is empty"))
			return
		}

		nonce := queryValues.Get("nonce")
		if nonce == "" {
			errHandler.ServeError(w, r, errors.New("nonce is empty"))
			return
		}

		encryptedMsg := queryValues.Get("echostr")
		if encryptedMsg == "" {
			errHandler.ServeError(w, r, errors.New("echostr is empty"))
			return
		}

		msgSignature2 := util.MsgSign(srv.Token(), timestamp, nonce, encryptedMsg)
		if subtle.ConstantTimeCompare([]byte(msgSignature1), []byte(msgSignature2)) != 1 {
			err := fmt.Errorf("check msg_signature failed, input: %s, local: %s", msgSignature1, msgSignature2)
			errHandler.ServeError(w, r, err)
			return
		}

		// 解密
		encryptedMsgBytes, err := base64.StdEncoding.DecodeString(encryptedMsg)
		if err != nil {
			errHandler.ServeError(w, r, err)
			return
		}

		corpId := srv.CorpId()
		aesKey := srv.CurrentAESKey()
		_, echostr, err := util.AESDecryptMsg(encryptedMsgBytes, corpId, aesKey)
		if err != nil {
			errHandler.ServeError(w, r, err)
			return
		}

		w.Write(echostr)
	}
}
