package core

import "testing"

func TestTextsForNotificationCopy(t *testing.T) {
	zh := TextsFor("zh")
	if zh.NotifyDone != "任务完成" || zh.NotifyFailed != "任务失败" {
		t.Fatalf("zh notification copy = %+v", zh)
	}
	if zh.NotifyCancelled != "任务已取消" ||
		zh.NotifyInterrupted != "任务已中断" ||
		zh.NotifyInteract != "需要你的输入" {
		t.Fatalf("zh notification copy = %+v", zh)
	}
	en := TextsFor("en")
	if en.NotifyDone != "Task finished" ||
		en.NotifyFailed != "Task failed" ||
		en.NotifyCancelled != "Task cancelled" ||
		en.NotifyInterrupted != "Task interrupted" ||
		en.NotifyInteract != "Input needed" {
		t.Fatalf("en notification copy = %+v", en)
	}
}
