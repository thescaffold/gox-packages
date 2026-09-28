package tests

import (
	"testing"

	test "github.com/awesome-goose/goose/testing"
	"github.com/thescaffold/gox-packages/libs/core/actor"
)

func TestActor(t *testing.T) {
	test.NewSuiteRunner(t, &ActorSuite{}).Run()
}

type ActorSuite struct{ test.Suite }

func (s *ActorSuite) TestValid_AcceptsEveryRealType() {
	s.T.Expect(actor.Actor{Type: actor.TypeUser, Id: "u-1"}.Valid()).ToEqual(true)
	s.T.Expect(actor.Actor{Type: actor.TypeAI, Id: "builder"}.Valid()).ToEqual(true)
	s.T.Expect(actor.Actor{Type: actor.TypeSystem, Id: "cron"}.Valid()).ToEqual(true)
}

func (s *ActorSuite) TestValid_RejectsEmptyId() {
	s.T.Expect(actor.Actor{Type: actor.TypeUser, Id: ""}.Valid()).ToEqual(false)
}

func (s *ActorSuite) TestValid_RejectsUnknownType() {
	s.T.Expect(actor.Actor{Type: "robot", Id: "r-1"}.Valid()).ToEqual(false)
}
