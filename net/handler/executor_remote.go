package cherryHandler

import (
	"fmt"
	ccode "github.com/cherry-game/cherry/code"
	cerr "github.com/cherry-game/cherry/error"
	ccrypto "github.com/cherry-game/cherry/extend/crypto"
	cfacade "github.com/cherry-game/cherry/facade"
	clog "github.com/cherry-game/cherry/logger"
	cmessage "github.com/cherry-game/cherry/net/message"
	cproto "github.com/cherry-game/cherry/net/proto"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap/zapcore"
	"reflect"
	"runtime/debug"
)

type (
	ExecutorRemote struct {
		Executor
		cfacade.IApplication
		handlerFn *cfacade.MethodInfo
		rt        *cmessage.Route
		data      []byte
		natsMsg   *nats.Msg
	}
)

func (p *ExecutorRemote) Route() *cmessage.Route {
	return p.rt
}

func (p *ExecutorRemote) Data() []byte {
	return p.data
}

func (p *ExecutorRemote) invoke0() []reflect.Value {
	var params []reflect.Value
	ret := p.handlerFn.Value.Call(params)
	if clog.PrintLevel(zapcore.DebugLevel) {
		clog.Debugf("[remote0] [route = %s, req = null, rsp = %+v",
			p.rt.String(),
			printRet(ret),
		)
	}
	return ret
}

func (p *ExecutorRemote) invoke1() []reflect.Value {
	val, err := p.UnmarshalData()
	if err != nil {
		clog.Warnf("[remote1] unmarshal data error. [route = %s, error = %s]",
			p.rt.String(),
			err,
		)
		return nil
	}

	var params []reflect.Value
	params = make([]reflect.Value, 1)
	params[0] = reflect.ValueOf(val)

	ret := p.handlerFn.Value.Call(params)
	if clog.PrintLevel(zapcore.DebugLevel) {
		clog.Debugf("[remote1] call result. [route = %s, req = %v, rsp = %+v]",
			p.rt.String(),
			params[0],
			printRet(ret),
		)
	}

	return ret
}

func (p *ExecutorRemote) Invoke() {
	defer func() {
		if rev := recover(); rev != nil {
			clog.Warnf("[remote] recover in Remote. %s", string(debug.Stack()))
			clog.Warnf("[route = %s,data = %v]", p.rt.String(), p.data)
		}
	}()

	argsLen := len(p.handlerFn.InArgs)
	if argsLen < 0 || argsLen > 1 {
		clog.Warnf("[remote] method in args error. [route = %s]", p.rt.String())
		clog.Warnf("func() or func(request)")

		p.natsResponse(&cproto.Response{
			Code: ccode.RPCRemoteExecuteError,
		})
		return
	}

	var ret []reflect.Value

	if argsLen == 0 {
		ret = p.invoke0()
	} else if argsLen == 1 {
		ret = p.invoke1()
	}

	p.response(ret)
}

func (p *ExecutorRemote) response(ret []reflect.Value) {
	if ret == nil {
		p.natsResponse(&cproto.Response{
			Code: ccode.RPCRemoteExecuteError,
		})
		return
	}

	rsp := &cproto.Response{
		Code: ccode.OK,
	}

	if len(ret) == 1 {
		if val := ret[0].Interface(); val != nil {
			if code, ok := val.(int32); ok {
				rsp.Code = code
			}
		}
	} else if len(ret) == 2 {
		if val := ret[1].Interface(); val != nil {
			if code, ok := val.(int32); ok {
				rsp.Code = code
			}
		}

		if ret[0].IsNil() == false {
			data, err := p.Marshal(ret[0].Interface())
			if err != nil {
				rsp.Code = ccode.RPCRemoteExecuteError
				clog.Warn(err)
			} else {
				rsp.Data = data
			}
		}
	}

	p.natsResponse(rsp)
}

func (p *ExecutorRemote) natsResponse(rsp *cproto.Response) {
	// ignore reply
	if p.natsMsg.Reply == "" {
		return
	}

	rspData, _ := p.Marshal(rsp)
	err := p.natsMsg.Respond(rspData)
	if err != nil {
		clog.Warn(err)
	}
}

func (p *ExecutorRemote) UnmarshalData() (interface{}, error) {
	if len(p.handlerFn.InArgs) != 1 {
		return nil, cerr.Error("[remote] handler params len is error.")
	}

	in2 := p.handlerFn.InArgs[0]

	var val interface{}
	val = reflect.New(in2.Elem()).Interface()
	err := p.Unmarshal(p.data, val)
	if err != nil {
		return nil, err
	}

	return val, err
}

func printRet(t []reflect.Value) interface{} {
	switch len(t) {
	case 1:
		{
			return fmt.Sprintf("%v", t[0].Interface())
		}
	case 2:
		{
			return fmt.Sprintf("%v, %v", t[0].Interface(), t[0].Interface())
		}
	}

	return ""
}

func (p *ExecutorRemote) QueueHash(queueNum int) int {
	hash := ccrypto.CRC32(p.rt.String())
	return hash % queueNum
}
