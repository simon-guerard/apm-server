// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package transaction

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elastic/beats/v7/libbeat/common"

	"github.com/elastic/apm-server/model"
	"github.com/elastic/apm-server/model/metadata"
	"github.com/elastic/apm-server/tests"
	"github.com/elastic/apm-server/transform"
	"github.com/elastic/apm-server/utility"
)

func TestTransactionEventDecodeFailure(t *testing.T) {
	for name, test := range map[string]struct {
		input interface{}
		err   error
		e     *Event
	}{
		"no input":           {input: nil, err: errMissingInput, e: nil},
		"invalid type":       {input: "", err: errInvalidType, e: nil},
		"cannot fetch field": {input: map[string]interface{}{}, err: utility.ErrFetch, e: nil},
	} {
		t.Run(name, func(t *testing.T) {
			transformable, err := DecodeEvent(model.Input{Raw: test.input})
			assert.Equal(t, test.err, err)
			if test.e != nil {
				event := transformable.(*Event)
				assert.Equal(t, test.e, event)
			} else {
				assert.Nil(t, transformable)
			}
		})
	}
}

func TestTransactionDecodeRUMV3Marks(t *testing.T) {
	// unknown fields are ignored
	input := map[string]interface{}{
		"k": map[string]interface{}{
			"foo": 0,
			"a": map[string]interface{}{
				"foo": 0,
				"dc":  1.2,
			},
			"nt": map[string]interface{}{
				"foo": 0,
				"dc":  1.2,
			},
		},
	}
	event := &Event{}
	transformable, err := decodeRUMV3Marks(event, input, model.Config{HasShortFieldNames: true})
	require.Nil(t, err)

	var f = 1.2
	assert.Equal(t, common.MapStr{
		"agent":            common.MapStr{"domComplete": &f},
		"navigationTiming": common.MapStr{"domComplete": &f},
	}, transformable.(*Event).Marks)
}

func TestTransactionEventDecode(t *testing.T) {
	id, trType, name, result := "123", "type", "foo()", "555"
	requestTime := time.Now()
	timestampParsed := time.Date(2017, 5, 30, 18, 53, 27, 154*1e6, time.UTC)
	timestampEpoch := json.Number(fmt.Sprintf("%d", timestampParsed.UnixNano()/1000))

	traceId, parentId := "0147258369012345abcdef0123456789", "abcdef0123456789"
	dropped, started, duration := 12, 6, 1.67
	name, userId, email, userIp := "jane", "abc123", "j@d.com", "127.0.0.1"
	url, referer, origUrl := "https://mypage.com", "http:mypage.com", "127.0.0.1"
	marks := map[string]interface{}{"k": "b"}
	sampled := true
	labels := model.Labels{"foo": "bar"}
	ua := "go-1.1"
	user := metadata.User{Name: &name, Email: &email, IP: net.ParseIP(userIp), Id: &userId, UserAgent: &ua}
	page := model.Page{Url: &url, Referer: &referer}
	request := model.Req{Method: "post", Socket: &model.Socket{}, Headers: http.Header{"User-Agent": []string{ua}}}
	response := model.Resp{Finished: new(bool), MinimalResp: model.MinimalResp{Headers: http.Header{"Content-Type": []string{"text/html"}}}}
	h := model.Http{Request: &request, Response: &response}
	ctxUrl := model.Url{Original: &origUrl}
	custom := model.Custom{"abc": 1}
	metadata := metadata.Metadata{Service: &metadata.Service{Name: tests.StringPtr("foo")}}

	for name, test := range map[string]struct {
		input interface{}
		cfg   model.Config
		err   string
		e     *Event
	}{
		"no timestamp specified, request time used": {
			input: map[string]interface{}{
				"id": id, "type": trType, "name": name, "duration": duration, "trace_id": traceId,
			},
			e: &Event{
				Metadata:  metadata,
				Id:        id,
				Type:      trType,
				Name:      &name,
				TraceId:   traceId,
				Duration:  duration,
				Timestamp: requestTime,
			},
		},
		"event experimental=true, no experimental payload": {
			input: map[string]interface{}{
				"id": id, "type": trType, "name": name, "duration": duration, "trace_id": traceId,
				"timestamp": timestampEpoch, "context": map[string]interface{}{"foo": "bar"},
			},
			cfg: model.Config{Experimental: true},
			e: &Event{
				Metadata:  metadata,
				Id:        id,
				Type:      trType,
				Name:      &name,
				TraceId:   traceId,
				Duration:  duration,
				Timestamp: timestampParsed,
			},
		},
		"event experimental=false": {
			input: map[string]interface{}{
				"id": id, "type": trType, "name": name, "duration": duration, "trace_id": traceId, "timestamp": timestampEpoch,
				"context": map[string]interface{}{"experimental": map[string]interface{}{"foo": "bar"}},
			},
			cfg: model.Config{Experimental: false},
			e: &Event{
				Metadata:  metadata,
				Id:        id,
				Type:      trType,
				Name:      &name,
				TraceId:   traceId,
				Duration:  duration,
				Timestamp: timestampParsed,
			},
		},
		"event experimental=true": {
			input: map[string]interface{}{
				"id": id, "type": trType, "name": name, "duration": duration, "trace_id": traceId, "timestamp": timestampEpoch,
				"context": map[string]interface{}{"experimental": map[string]interface{}{"foo": "bar"}},
			},
			cfg: model.Config{Experimental: true},
			e: &Event{
				Metadata:     metadata,
				Id:           id,
				Type:         trType,
				Name:         &name,
				TraceId:      traceId,
				Duration:     duration,
				Timestamp:    timestampParsed,
				Experimental: map[string]interface{}{"foo": "bar"},
			},
		},
		"messaging event": {
			input: map[string]interface{}{
				"id":        id,
				"trace_id":  traceId,
				"duration":  duration,
				"timestamp": timestampEpoch,
				"type":      "messaging",
				"context": map[string]interface{}{
					"message": map[string]interface{}{
						"queue":   map[string]interface{}{"name": "order"},
						"body":    "confirmed",
						"headers": map[string]interface{}{"internal": "false"},
						"age":     map[string]interface{}{"ms": json.Number("1577958057123")}}}},
			e: &Event{
				Metadata:  metadata,
				Id:        id,
				Type:      "messaging",
				TraceId:   traceId,
				Duration:  duration,
				Timestamp: timestampParsed,
				Message: &model.Message{
					QueueName: tests.StringPtr("order"),
					Body:      tests.StringPtr("confirmed"),
					Headers:   http.Header{"Internal": []string{"false"}},
					AgeMillis: tests.IntPtr(1577958057123),
				},
			},
		},
		"valid event": {
			input: map[string]interface{}{
				"id": id, "type": trType, "name": name, "result": result,
				"duration": duration, "timestamp": timestampEpoch,
				"context": map[string]interface{}{
					"a":      "b",
					"custom": map[string]interface{}{"abc": 1},
					"user":   map[string]interface{}{"username": name, "email": email, "ip": userIp, "id": userId},
					"tags":   map[string]interface{}{"foo": "bar"},
					"page":   map[string]interface{}{"url": url, "referer": referer},
					"request": map[string]interface{}{
						"method":  "POST",
						"url":     map[string]interface{}{"raw": "127.0.0.1"},
						"headers": map[string]interface{}{"user-agent": ua}},
					"response": map[string]interface{}{
						"finished": false,
						"headers":  map[string]interface{}{"Content-Type": "text/html"}},
				},
				"marks": marks, "sampled": sampled,
				"parent_id": parentId, "trace_id": traceId,
				"spans": []interface{}{
					map[string]interface{}{
						"name": "span", "type": "db", "start": 1.2, "duration": 2.3,
					}},
				"span_count": map[string]interface{}{"dropped": 12.0, "started": 6.0}},
			e: &Event{
				Metadata:  metadata,
				Id:        id,
				Type:      trType,
				Name:      &name,
				Result:    &result,
				ParentId:  &parentId,
				TraceId:   traceId,
				Duration:  duration,
				Timestamp: timestampParsed,
				Marks:     marks,
				Sampled:   &sampled,
				SpanCount: SpanCount{Dropped: &dropped, Started: &started},
				User:      &user,
				Labels:    &labels,
				Page:      &page,
				Custom:    &custom,
				Http:      &h,
				Url:       &ctxUrl,
				Client:    &model.Client{IP: net.ParseIP(userIp)},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			transformable, err := DecodeEvent(model.Input{
				Raw:         test.input,
				RequestTime: requestTime,
				Metadata:    metadata,
				Config:      test.cfg,
			})
			if test.err != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), test.err)
			}
			if test.e != nil && assert.NotNil(t, transformable) {
				event := transformable.(*Event)
				assert.Equal(t, test.e, event)
			}
		})
	}
}

func TestEventTransform(t *testing.T) {
	id := "123"
	result := "tx result"
	sampled := false
	dropped, startedSpans := 5, 14
	name := "mytransaction"

	tests := []struct {
		Event  Event
		Output common.MapStr
		Msg    string
	}{
		{
			Event: Event{},
			Output: common.MapStr{
				"id":       "",
				"type":     "",
				"duration": common.MapStr{"us": 0},
				"sampled":  true,
			},
			Msg: "Empty Event",
		},
		{
			Event: Event{
				Id:       id,
				Type:     "tx",
				Duration: 65.98,
			},
			Output: common.MapStr{
				"id":       id,
				"type":     "tx",
				"duration": common.MapStr{"us": 65980},
				"sampled":  true,
			},
			Msg: "SpanCount empty",
		},
		{
			Event: Event{
				Id:        id,
				Type:      "tx",
				Duration:  65.98,
				SpanCount: SpanCount{Started: &startedSpans},
			},
			Output: common.MapStr{
				"id":         id,
				"type":       "tx",
				"duration":   common.MapStr{"us": 65980},
				"span_count": common.MapStr{"started": 14},
				"sampled":    true,
			},
			Msg: "SpanCount only contains `started`",
		},
		{
			Event: Event{
				Id:        id,
				Type:      "tx",
				Duration:  65.98,
				SpanCount: SpanCount{Dropped: &dropped},
			},
			Output: common.MapStr{
				"id":         id,
				"type":       "tx",
				"duration":   common.MapStr{"us": 65980},
				"span_count": common.MapStr{"dropped": 5},
				"sampled":    true,
			},
			Msg: "SpanCount only contains `dropped`",
		},
		{
			Event: Event{
				Id:        id,
				Name:      &name,
				Type:      "tx",
				Result:    &result,
				Timestamp: time.Now(),
				Duration:  65.98,
				Sampled:   &sampled,
				SpanCount: SpanCount{Started: &startedSpans, Dropped: &dropped},
			},
			Output: common.MapStr{
				"id":         id,
				"name":       "mytransaction",
				"type":       "tx",
				"result":     "tx result",
				"duration":   common.MapStr{"us": 65980},
				"span_count": common.MapStr{"started": 14, "dropped": 5},
				"sampled":    false,
			},
			Msg: "Full Event",
		},
	}

	tctx := &transform.Context{}

	for idx, test := range tests {
		output := test.Event.Transform(context.Background(), tctx)
		assert.Equal(t, test.Output, output[0].Fields["transaction"], fmt.Sprintf("Failed at idx %v; %s", idx, test.Msg))
	}
}

func TestEventsTransformWithMetadata(t *testing.T) {
	hostname := "a.b.c"
	architecture := "darwin"
	platform := "x64"
	timestamp := time.Date(2019, 1, 3, 15, 17, 4, 908.596*1e6, time.FixedZone("+0100", 3600))
	timestampUs := timestamp.UnixNano() / 1000
	id, name, ip, userAgent := "123", "jane", "63.23.123.4", "node-js-2.3"
	user := metadata.User{Id: &id, Name: &name, IP: net.ParseIP(ip), UserAgent: &userAgent}
	url, referer := "https://localhost", "http://localhost"
	serviceName, serviceNodeName, serviceVersion := "myservice", "service-123", "2.1.3"
	eventMetadata := metadata.Metadata{
		Service: &metadata.Service{
			Name: &serviceName,
			Node: metadata.ServiceNode{Name: tests.StringPtr(serviceNodeName)},
		},
		System: &metadata.System{
			ConfiguredHostname: &name,
			DetectedHostname:   &hostname,
			Architecture:       &architecture,
			Platform:           &platform,
		},
		Labels: common.MapStr{"a": true},
	}

	txValid := Event{Metadata: eventMetadata, Timestamp: timestamp}
	events := txValid.Transform(context.Background(), &transform.Context{})
	require.Len(t, events, 1)
	assert.Equal(t, events[0].Fields, common.MapStr{
		"processor": common.MapStr{
			"event": "transaction",
			"name":  "transaction",
		},
		"service": common.MapStr{
			"name": serviceName,
			"node": common.MapStr{
				"name": serviceNodeName,
			},
		},
		"host": common.MapStr{
			"architecture": "darwin",
			"hostname":     "a.b.c",
			"name":         "jane",
			"os": common.MapStr{
				"platform": "x64",
			},
		},
		"transaction": common.MapStr{
			"duration": common.MapStr{"us": 0},
			"id":       "",
			"type":     "",
			"sampled":  true,
		},
		"labels":    common.MapStr{"a": true},
		"timestamp": common.MapStr{"us": timestampUs},
	})

	request := model.Req{Method: "post", Socket: &model.Socket{}, Headers: http.Header{}}
	response := model.Resp{Finished: new(bool), MinimalResp: model.MinimalResp{Headers: http.Header{"content-type": []string{"text/html"}}}}
	txWithContext := Event{
		Metadata:  eventMetadata,
		Timestamp: timestamp,
		User:      &user,
		Labels:    &model.Labels{"a": "b"},
		Page:      &model.Page{Url: &url, Referer: &referer},
		Http:      &model.Http{Request: &request, Response: &response},
		Url:       &model.Url{Original: &url},
		Custom:    &model.Custom{"foo": "bar"},
		Client:    &model.Client{IP: net.ParseIP("198.12.13.1")},
		Message:   &model.Message{QueueName: tests.StringPtr("routeUser")},
		Service:   &metadata.Service{Version: &serviceVersion},
	}
	events = txWithContext.Transform(context.Background(), &transform.Context{})
	require.Len(t, events, 1)
	assert.Equal(t, events[0].Fields, common.MapStr{
		"user":       common.MapStr{"id": "123", "name": "jane"},
		"client":     common.MapStr{"ip": "198.12.13.1"},
		"source":     common.MapStr{"ip": "198.12.13.1"},
		"user_agent": common.MapStr{"original": userAgent},
		"host": common.MapStr{
			"architecture": "darwin",
			"hostname":     "a.b.c",
			"name":         "jane",
			"os": common.MapStr{
				"platform": "x64",
			},
		},
		"processor": common.MapStr{
			"event": "transaction",
			"name":  "transaction",
		},
		"service": common.MapStr{
			"name":    serviceName,
			"version": serviceVersion,
			"node":    common.MapStr{"name": serviceNodeName},
		},
		"timestamp": common.MapStr{"us": timestampUs},
		"transaction": common.MapStr{
			"duration": common.MapStr{"us": 0},
			"id":       "",
			"type":     "",
			"sampled":  true,
			"page":     common.MapStr{"url": url, "referer": referer},
			"custom": common.MapStr{
				"foo": "bar",
			},
			"message": common.MapStr{"queue": common.MapStr{"name": "routeUser"}},
		},
		"labels": common.MapStr{"a": "b"},
		"url":    common.MapStr{"original": url},
		"http": common.MapStr{
			"request":  common.MapStr{"method": "post"},
			"response": common.MapStr{"finished": false, "headers": common.MapStr{"content-type": []string{"text/html"}}}},
	})
}
