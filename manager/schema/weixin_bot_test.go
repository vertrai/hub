package schema

import (
	"reflect"
	"strings"
	"testing"
)

func TestWeixinBotUsesSeparateAssignmentAndResetReservationColumns(t *testing.T) {
	typ := reflect.TypeOf(WeixinBot{})
	assigned, ok := typ.FieldByName("AssignedPodID")
	if !ok || !strings.Contains(assigned.Tag.Get("gorm"), "uniqueIndex") {
		t.Fatal("AssignedPodID must remain the unique active assignment")
	}
	reserved, ok := typ.FieldByName("ResetPodID")
	if !ok {
		t.Fatal("ResetPodID must store the asynchronous replacement reservation")
	}
	if tag := reserved.Tag.Get("gorm"); !strings.Contains(tag, "index") || strings.Contains(tag, "uniqueIndex") {
		t.Fatalf("ResetPodID must be indexed but non-unique, tag=%q", tag)
	}
}
