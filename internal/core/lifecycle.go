package core

import "time"

func (runtime *Runtime) sourceFinished(id string) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if item := runtime.sessions[id]; item != nil {
		item.sourceOps--
		item.info.LastActivity = time.Now().UTC()
	}
}

func (runtime *Runtime) reapLoop() {
	defer close(runtime.reaperDone)
	interval := time.Duration(runtime.config.IdleTimeoutMS) * time.Millisecond / 4
	if interval > time.Second {
		interval = time.Second
	}
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			runtime.reapIdle(now)
		case <-runtime.ctx.Done():
			return
		}
	}
}

func (runtime *Runtime) reapIdle(now time.Time) {
	var debuggers []*Debugger
	runtime.mu.Lock()
	for id, item := range runtime.sessions {
		if now.Sub(item.info.LastActivity) < time.Duration(runtime.config.IdleTimeoutMS)*time.Millisecond {
			continue
		}
		if item.info.Status == "CLOSED" {
			delete(runtime.sessions, id)
			continue
		}
		if (item.info.Status == "READY" || item.info.Status == "FAILED") && item.sourceOps == 0 {
			item.info.Status = "CLOSED"
			item.info.LastActivity = now
			item.cancel()
			if item.debugger != nil {
				debuggers = append(debuggers, item.debugger)
				item.debugger = nil
			}
		}
	}
	runtime.mu.Unlock()
	for _, debugger := range debuggers {
		debugger.Close()
	}
}
