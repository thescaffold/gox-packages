package tests

import (
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages-core/ws"
)

func TestWs(t *testing.T) {
	test.NewSuiteRunner(t, &WsSuite{}).Run()
}

type WsSuite struct {
	test.Suite
}

func (s *WsSuite) TestEmit_NoopReturnsNil() {
	svc := &ws.WsService{}
	err := svc.Emit("ns", "room1", "user.created", map[string]any{"id": "u1"})
	s.T.Expect(err).ToBeNil()
}

func (s *WsSuite) TestBind_NoopReturnsNil() {
	svc := &ws.WsService{}
	err := svc.Bind(":3000")
	s.T.Expect(err).ToBeNil()
}
