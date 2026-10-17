package transfer_server_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	transfer_server "github.com/ERONIS/wb-service/internal/feature/transfer/server"
)

// mockProcessor эмулирует этап пайплайна для тестирования polling.
type mockProcessor struct {
	calls atomic.Int64
}

func (m *mockProcessor) ProcessPending(ctx context.Context) error {
	m.calls.Add(1)
	return nil
}

// TestPolling_ChainedWake проверяет сквозное пробуждение downstream-обработчиков по цепочке.
func TestPolling_ChainedWake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wakeChan := make(chan struct{}, 1)

	p0 := &mockProgressProcessor{}
	p0.progress.Store(true)
	p1 := &mockProcessor{}

	polling := transfer_server.NewPollingWithWake(
		10*time.Second, // длинный интервал, чтобы срабатывал именно wake, а не тикер
		wakeChan,
		p0,
		p1,
	)

	errChan := make(chan error, 1)
	go func() {
		errChan <- polling.Run(ctx, func(err error) {
			t.Errorf("неожиданная ошибка polling: %v", err)
		})
	}()

	// Ждём завершения начального startup-цикла
	deadline := time.Now().Add(2 * time.Second)
	for p0.calls.Load() < 1 || p1.calls.Load() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("таймаут ожидания startup: p0=%d, p1=%d", p0.calls.Load(), p1.calls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}

	p0Initial := p0.calls.Load()
	p1Initial := p1.calls.Load()

	// Посылаем wake во внешний канал
	wakeChan <- struct{}{}

	// Ожидаем, что p0 получит wake, и следом за ним p1 будет разбужен по цепочке
	deadline = time.Now().Add(2 * time.Second)
	for p0.calls.Load() <= p0Initial || p1.calls.Load() <= p1Initial {
		if time.Now().After(deadline) {
			t.Fatalf("таймаут ожидания chained wake: p0 было=%d стало=%d, p1 было=%d стало=%d",
				p0Initial, p0.calls.Load(), p1Initial, p1.calls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("ожидалось завершение без ошибки, получено: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("polling не завершился после отмены контекста")
	}
}

// mockProgressProcessor эмулирует процессор с поддержкой ProgressAwareProcessor.
type mockProgressProcessor struct {
	calls    atomic.Int64
	progress atomic.Bool
}

func (m *mockProgressProcessor) ProcessPending(ctx context.Context) error {
	m.calls.Add(1)
	return nil
}

func (m *mockProgressProcessor) ProcessPendingWithProgress(ctx context.Context) (bool, error) {
	m.calls.Add(1)
	return m.progress.Load(), nil
}

// TestPolling_ProgressAwareWake проверяет, что downstream-этапы пробуждаются ТОЛЬКО при progress == true.
func TestPolling_ProgressAwareWake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wakeChan := make(chan struct{}, 1)

	p0 := &mockProgressProcessor{}
	p0.progress.Store(false) // Изначально прогресса нет (пустой прогон)
	p1 := &mockProcessor{}

	polling := transfer_server.NewPollingWithWake(
		10*time.Second,
		wakeChan,
		p0,
		p1,
	)

	errChan := make(chan error, 1)
	go func() {
		errChan <- polling.Run(ctx, func(err error) {
			t.Errorf("неожиданная ошибка polling: %v", err)
		})
	}()

	// Ждём завершения начального startup-цикла
	deadline := time.Now().Add(2 * time.Second)
	for p0.calls.Load() < 1 || p1.calls.Load() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("таймаут ожидания startup: p0=%d, p1=%d", p0.calls.Load(), p1.calls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}

	p1AfterStartup := p1.calls.Load()

	// 1. Посылаем сигнал в wakeChan при progress == false
	wakeChan <- struct{}{}

	// Ждём, пока p0 выполнится
	deadline = time.Now().Add(1 * time.Second)
	for p0.calls.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("p0 не отреагировал на wake")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Даём небольшую паузу и проверяем, что p1 НЕ был вызван (нет прогресса)
	time.Sleep(100 * time.Millisecond)
	if p1.calls.Load() != p1AfterStartup {
		t.Fatalf("p1 не должен был пробуждаться при отсутствии реального прогресса: p1=%d, ожидалось=%d",
			p1.calls.Load(), p1AfterStartup)
	}

	// 2. Теперь включаем реальный прогресс у p0
	p0.progress.Store(true)
	wakeChan <- struct{}{}

	// Ожидаем, что теперь p1 БУДЕТ разбужен
	deadline = time.Now().Add(2 * time.Second)
	for p1.calls.Load() <= p1AfterStartup {
		if time.Now().After(deadline) {
			t.Fatalf("таймаут ожидания пробуждения p1 при наличии прогресса: p1=%d", p1.calls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("ожидалось завершение без ошибки, получено: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("polling не завершился после отмены контекста")
	}
}
