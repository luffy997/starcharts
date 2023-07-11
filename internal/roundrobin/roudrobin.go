// Package roundrobin provides round robin invalidation-aware load balancing of github tokens.
// token 循环轮询算法设计
package roundrobin

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/apex/log"
)

// RoundRobiner can pick a token from a list of tokens.
type RoundRobiner interface {
	Pick() (*Token, error)
}

// New round robin implementation with the given list of tokens.
func New(tokens []string) RoundRobiner {
	log.Debugf("creating round robin with %d tokens", len(tokens))
	if len(tokens) == 0 {
		return &noTokensRoundRobin{}
	}
	result := make([]*Token, 0, len(tokens))
	for _, item := range tokens {
		result = append(result, NewToken(item))
	}
	return &realRoundRobin{tokens: result}
}

type realRoundRobin struct {
	tokens []*Token
	next   int64
}

func (rr *realRoundRobin) Pick() (*Token, error) {
	return rr.doPick(0)
}

// 达到负载均衡的函数，token循环使用
func (rr *realRoundRobin) doPick(try int) (*Token, error) {
	if try > len(rr.tokens) {
		return nil, fmt.Errorf("no valid tokens left")
	}
	// atomic 原子操作，确保在并发下不会受到别的realRoundRobin的干扰
	idx := atomic.LoadInt64(&rr.next)
	// 使用atomic.StoreInt64函数将新的值(idx+1)%int64(len(rr.tokens))存储到rr.next中。
	//它将当前索引加1，并使用len(rr.tokens)取模来实现循环。
	atomic.StoreInt64(&rr.next, (idx+1)%int64(len(rr.tokens)))
	if pick := rr.tokens[idx]; pick.OK() {
		// 拿到tokens中索引为idx的token，判断是否合法，合法返回
		log.Debugf("picked %s", pick.Key())
		return pick, nil
	}
	// 递归，直到tokens为空或者拿到合法token再退出
	return rr.doPick(try + 1)
}

type noTokensRoundRobin struct{}

func (rr *noTokensRoundRobin) Pick() (*Token, error) {
	return nil, nil
}

// Token is a github token.
type Token struct {
	token string
	valid bool
	lock  sync.RWMutex
}

// NewToken from its string representation.
func NewToken(token string) *Token {
	return &Token{
		token: token,
		valid: true,
	}
}

// String returns the last 3 chars for the token.
func (t *Token) String() string {
	return t.token[len(t.token)-3:]
}

// Key returns the actual token.
func (t *Token) Key() string {
	return t.token
}

// OK returns true if the token is valid.
func (t *Token) OK() bool {
	t.lock.RLock()
	defer t.lock.RUnlock()
	return t.valid
}

// Invalidate invalidates the token.
func (t *Token) Invalidate() {
	log.Warnf("invalidated token '...%s'", t)
	t.lock.Lock()
	defer t.lock.Unlock()
	t.valid = false
}
