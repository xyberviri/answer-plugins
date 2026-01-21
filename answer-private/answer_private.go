/*
 * Licensed to the Apache Software Foundation (ASF) under one
 * or more contributor license agreements.  See the NOTICE file
 * distributed with this work for additional information
 * regarding copyright ownership.  The ASF licenses this file
 * to you under the Apache License, Version 2.0 (the
 * "License"); you may not use this file except in compliance
 * with the License.  You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package answer_private

import (
	"bytes"
	"embed"
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/apache/answer-plugins/answer-private/i18n"
	"github.com/apache/answer-plugins/util"
	"github.com/apache/answer/plugin"
	"github.com/gin-gonic/gin"
)

//go:embed info.yaml
var Info embed.FS

const (
	pathAnswerInfo = "/answer/api/v1/answer/info"
	pathAnswerList = "/answer/api/v1/answer/page"
)

type Config struct {
	Enabled bool `json:"enabled"`
}

type AnswerPrivate struct {
	Config *Config
}

func init() {
	plugin.Register(&AnswerPrivate{
		Config: &Config{Enabled: true},
	})
}

func (a *AnswerPrivate) Info() plugin.Info {
	info := &util.Info{}
	info.GetInfo(Info)

	return plugin.Info{
		Name:        plugin.MakeTranslator(i18n.InfoName),
		SlugName:    info.SlugName,
		Description: plugin.MakeTranslator(i18n.InfoDescription),
		Author:      info.Author,
		Version:     info.Version,
		Link:        info.Link,
	}
}

func (a *AnswerPrivate) ConfigFields() []plugin.ConfigField {
	return []plugin.ConfigField{
		{
			Name:        "enabled",
			Type:        plugin.ConfigTypeSwitch,
			Title:       plugin.MakeTranslator(i18n.ConfigEnabledLabel),
			Description: plugin.MakeTranslator(i18n.ConfigEnabledDescription),
			Required:    false,
			Value:       a.Config.Enabled,
		},
	}
}

func (a *AnswerPrivate) ConfigReceiver(config []byte) error {
	c := &Config{Enabled: true}
	_ = json.Unmarshal(config, c)
	if c == nil {
		c = &Config{Enabled: true}
	}
	a.Config = c
	return nil
}

func (a *AnswerPrivate) RegisterUnAuthRouter(*gin.RouterGroup) {
	// This plugin only filters answer responses on authenticated routes.
}

func (a *AnswerPrivate) RegisterAuthUserRouter(r *gin.RouterGroup) {
	r.Use(a.answerVisibilityMiddleware())
}

func (a *AnswerPrivate) RegisterAuthAdminRouter(*gin.RouterGroup) {
	// Admin routes are not modified.
}

func (a *AnswerPrivate) answerVisibilityMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if a.Config == nil || !a.Config.Enabled {
			ctx.Next()
			return
		}

		path := ctx.Request.URL.Path
		if path != pathAnswerInfo && path != pathAnswerList {
			ctx.Next()
			return
		}

		userID := userIDFromContext(ctx)
		if userID == "" {
			ctx.Next()
			return
		}

		recorder := newResponseRecorder(ctx.Writer)
		ctx.Writer = recorder
		ctx.Next()

		status := recorder.statusCode()
		body := recorder.body.Bytes()

		filtered, newStatus, changed := filterAnswerResponse(path, userID, body)
		ctx.Writer = recorder.ResponseWriter
		if !changed {
			writeResponse(ctx, status, body)
			return
		}

		if newStatus != 0 {
			status = newStatus
		}
		writeResponse(ctx, status, filtered)
	}
}

type responseRecorder struct {
	gin.ResponseWriter
	status int
	body   bytes.Buffer
}

func newResponseRecorder(writer gin.ResponseWriter) *responseRecorder {
	return &responseRecorder{ResponseWriter: writer}
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
}

func (r *responseRecorder) WriteHeaderNow() {
	if r.status == 0 {
		r.status = http.StatusOK
	}
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	return r.body.Write(data)
}

func (r *responseRecorder) statusCode() int {
	if r.status != 0 {
		return r.status
	}
	return http.StatusOK
}

func writeResponse(ctx *gin.Context, status int, body []byte) {
	ctx.Writer.WriteHeader(status)
	_, _ = ctx.Writer.Write(body)
}

type responseEnvelope struct {
	Code    int             `json:"code"`
	Reason  string          `json:"reason"`
	Message string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

func filterAnswerResponse(path, userID string, body []byte) ([]byte, int, bool) {
	if len(body) == 0 {
		return nil, 0, false
	}

	var envelope responseEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, 0, false
	}

	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, 0, false
	}

	switch path {
	case pathAnswerInfo:
		return filterAnswerInfo(envelope, userID)
	case pathAnswerList:
		return filterAnswerList(envelope, userID)
	default:
		return nil, 0, false
	}
}

func filterAnswerInfo(envelope responseEnvelope, userID string) ([]byte, int, bool) {
	var payload map[string]any
	if err := json.Unmarshal(envelope.Data, &payload); err != nil {
		return nil, 0, false
	}

	answerOwnerID := extractAnswerOwnerID(payload)
	questionOwnerID := extractQuestionOwnerID(payload)
	if answerOwnerID == "" && questionOwnerID == "" {
		return nil, 0, false
	}

	if answerOwnerID == userID || questionOwnerID == userID {
		return nil, 0, false
	}

	forbidden := responseEnvelope{
		Code:    http.StatusForbidden,
		Reason:  "answer_private_forbidden",
		Message: "answer is private",
		Data:    json.RawMessage("null"),
	}
	payloadBytes, err := json.Marshal(forbidden)
	if err != nil {
		return nil, 0, false
	}
	return payloadBytes, http.StatusForbidden, true
}

func filterAnswerList(envelope responseEnvelope, userID string) ([]byte, int, bool) {
	var payload map[string]any
	if err := json.Unmarshal(envelope.Data, &payload); err != nil {
		return nil, 0, false
	}

	list, ok := payload["list"].([]any)
	if !ok {
		return nil, 0, false
	}

	filtered := make([]any, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		answerOwnerID := extractAnswerOwnerID(entry)
		questionOwnerID := extractQuestionOwnerID(entry)
		if answerOwnerID == "" && questionOwnerID == "" {
			continue
		}
		if answerOwnerID == userID || questionOwnerID == userID {
			filtered = append(filtered, entry)
		}
	}

	payload["list"] = filtered
	payload["count"] = len(filtered)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, false
	}

	envelope.Data = payloadBytes
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, 0, false
	}
	return body, 0, true
}

func extractAnswerOwnerID(payload map[string]any) string {
	info, ok := payload["info"].(map[string]any)
	if ok {
		return extractUserIDFromInfo(info)
	}

	return extractUserIDFromInfo(payload)
}

func extractQuestionOwnerID(payload map[string]any) string {
	question, ok := payload["question"].(map[string]any)
	if ok {
		return extractUserIDFromInfo(question)
	}
	questionInfo, ok := payload["question_info"].(map[string]any)
	if ok {
		return extractUserIDFromInfo(questionInfo)
	}
	return ""
}

func extractUserIDFromInfo(info map[string]any) string {
	userInfo, ok := info["user_info"].(map[string]any)
	if !ok {
		return ""
	}
	id, _ := userInfo["id"].(string)
	return id
}

func userIDFromContext(ctx *gin.Context) string {
	userInfo, ok := ctx.Get("ctxUuidKey")
	if !ok || userInfo == nil {
		return ""
	}

	userInfoValue := reflectValue(userInfo)
	if !userInfoValue.IsValid() {
		return ""
	}

	field := userInfoValue.FieldByName("UserID")
	if !field.IsValid() || field.Kind() != reflect.String {
		return ""
	}

	return field.String()
}

func reflectValue(value any) reflect.Value {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Ptr {
		return v.Elem()
	}
	return v
}
