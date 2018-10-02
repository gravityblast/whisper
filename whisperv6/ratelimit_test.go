package whisperv6

import (
	"math"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/discover"
	"github.com/status-im/status-go/ratelimiter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/storage"
)

const (
	testCode = 42 // any non-defined code will work
)

func setupOneConnection(t *testing.T, rlconf ratelimiter.Config) (*Whisper, *p2p.MsgPipeRW, chan error) {
	db, err := leveldb.Open(storage.NewMemStorage(), nil)
	require.NoError(t, err)
	rl := ratelimiter.ForWhisper(ratelimiter.IDMode, db, rlconf, rlconf)
	conf := &Config{
		MinimumAcceptedPOW: 0,
		MaxMessageSize:     100 << 10,
	}
	w := New(conf)
	w.UseRateLimiter(rl)
	idx, _ := discover.BytesID([]byte{0x01})
	p := p2p.NewPeer(idx, "1", []p2p.Cap{{"shh", 6}})
	rw1, rw2 := p2p.MsgPipe()
	errorc := make(chan error, 1)
	go func() {
		errorc <- w.HandlePeer(p, rw2)
	}()
	msg, err := rw1.ReadMsg()
	require.NoError(t, err)
	require.Equal(t, uint64(0), msg.Code)
	require.NoError(t, msg.Discard())
	require.NoError(t, p2p.SendItems(rw1, statusCode, ProtocolVersion, math.Float64bits(w.MinPow()), w.BloomFilter(), true))
	require.NoError(t, p2p.ExpectMsg(rw1, peerRateLimitCode, nil), "peer must send ingress rate limit after handshake")
	return w, rw1, errorc
}

func TestRatePeerDropsConnection(t *testing.T) {
	cfg := ratelimiter.Config{Interval: uint64(time.Hour), Capacity: 10 << 10, Quantum: 1 << 10}
	_, rw1, errorc := setupOneConnection(t, cfg)

	require.NoError(t, p2p.Send(rw1, testCode, make([]byte, 11<<10))) // limit is 1024
	select {
	case err := <-errorc:
		require.Error(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "failed waiting for HandlePeer to exit")
	}
}

func TestRateLimitedDelivery(t *testing.T) {
	cfg := ratelimiter.Config{Interval: uint64(time.Hour), Capacity: 10 << 10, Quantum: 1 << 10}
	w, rw1, _ := setupOneConnection(t, cfg)
	small1 := Envelope{
		Expiry: uint32(time.Now().Add(10 * time.Second).Unix()),
		TTL:    10,
		Topic:  TopicType{1},
		Data:   make([]byte, 1<<10),
		Nonce:  1,
	}
	small2 := small1
	small2.Nonce = 2
	big := small1
	big.Nonce = 3
	big.Data = make([]byte, 11<<10)

	require.NoError(t, w.Send(&small1))
	require.NoError(t, w.Send(&big))
	require.NoError(t, w.Send(&small2))

	received := map[common.Hash]struct{}{}
	// we can not guarantee that all expected envelopes will be delivered in a one batch
	// so allow whisper to write multiple times and read every message
	go func() {
		time.Sleep(time.Second)
		rw1.Close()
	}()
	for {
		msg, err := rw1.ReadMsg()
		if err == p2p.ErrPipeClosed {
			require.Contains(t, received, small1.Hash())
			require.Contains(t, received, small2.Hash())
			require.NotContains(t, received, big.Hash())
			break
		}
		require.NoError(t, err)
		require.Equal(t, uint64(1), msg.Code)
		var rst []*Envelope
		require.NoError(t, msg.Decode(&rst))
		for _, e := range rst {
			received[e.Hash()] = struct{}{}
		}
	}
}

func TestRateRandomizedDelivery(t *testing.T) {
	cfg := ratelimiter.Config{Interval: uint64(time.Hour), Capacity: 10 << 10, Quantum: 1 << 10}
	w1, rw1, _ := setupOneConnection(t, cfg)
	w2, rw2, _ := setupOneConnection(t, cfg)
	w3, rw3, _ := setupOneConnection(t, cfg)
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		sent     = map[common.Hash]int{}
		received = map[int]int64{}
	)
	for i := uint64(1); i < 15; i++ {
		env := &Envelope{
			Expiry: uint32(time.Now().Add(10 * time.Second).Unix()),
			TTL:    10,
			Topic:  TopicType{1},
			Data:   make([]byte, 1<<10-EnvelopeHeaderLength), // so that 10 envelopes are exactly 10kb
			Nonce:  i,
		}
		sent[env.Hash()] = 0
		for _, w := range []*Whisper{w1, w2, w3} {
			go func(w *Whisper, e *Envelope) {
				time.Sleep(time.Duration(rand.Int63n(10)) * time.Millisecond)
				assert.NoError(t, w.Send(e))
			}(w, env)
		}
	}
	for i, rw := range []*p2p.MsgPipeRW{rw1, rw2, rw3} {
		received[i] = 0
		wg.Add(1)
		go func(rw *p2p.MsgPipeRW) {
			time.Sleep(time.Second)
			rw.Close()
			wg.Done()
		}(rw)
		wg.Add(1)
		go func(i int, rw *p2p.MsgPipeRW) {
			defer wg.Done()
			for {
				msg, err := rw.ReadMsg()
				if err != nil {
					return
				}
				if !assert.Equal(t, uint64(1), msg.Code) {
					return
				}
				var rst []*Envelope
				if !assert.NoError(t, msg.Decode(&rst)) {
					return
				}
				mu.Lock()
				for _, e := range rst {
					received[i] += int64(len(e.Data))
					received[i] += EnvelopeHeaderLength
					sent[e.Hash()]++
				}
				mu.Unlock()
			}
		}(i, rw)
	}
	wg.Wait()
	for i := range received {
		require.Equal(t, int64(10)<<10, received[i], "peer %d didnt' receive 10 kb of data: %d", i, received[i])
	}
	total := 0
	for h := range sent {
		total += sent[h]
	}
	require.Equal(t, 30, total)
}
