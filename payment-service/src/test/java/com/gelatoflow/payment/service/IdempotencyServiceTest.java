package com.gelatoflow.payment.service;

import static org.assertj.core.api.Assertions.assertThat;

import java.time.Duration;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicInteger;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

class IdempotencyServiceTest {
  private IdempotencyService service;

  @BeforeEach
  void setUp() {
    service = new IdempotencyService();
  }

  @Test
  void recordsNewPaymentIntentAndRejectsDuplicate() {
    String intentId = "pi_3MtwBwLkdIwHu7ix28a3tqPa";

    assertThat(service.isProcessed(intentId)).isFalse();
    assertThat(service.isDuplicate(intentId)).isFalse();

    boolean firstResult = service.recordIfAbsent(intentId);
    assertThat(firstResult).isTrue();

    assertThat(service.isProcessed(intentId)).isTrue();
    assertThat(service.isDuplicate(intentId)).isTrue();

    boolean secondResult = service.recordIfAbsent(intentId);
    assertThat(secondResult).isFalse();
  }

  @Test
  void markProcessedSetsIntentAsProcessed() {
    String intentId = "pi_mark_processed_test";

    assertThat(service.isProcessed(intentId)).isFalse();

    service.markProcessed(intentId);

    assertThat(service.isProcessed(intentId)).isTrue();
    assertThat(service.recordIfAbsent(intentId)).isFalse();
  }

  @Test
  void nullAndBlankIntentsAreSafelyIgnored() {
    assertThat(service.recordIfAbsent(null)).isFalse();
    assertThat(service.recordIfAbsent("")).isFalse();
    assertThat(service.recordIfAbsent("   ")).isFalse();

    assertThat(service.isProcessed(null)).isFalse();
    assertThat(service.isProcessed("")).isFalse();

    service.markProcessed(null);
    service.markProcessed("");
    assertThat(service.size()).isZero();
  }

  @Test
  void entriesExpireAfterTtl() throws InterruptedException {
    Duration shortTtl = Duration.ofMillis(50);
    IdempotencyService shortTtlService = new IdempotencyService(shortTtl, 100);

    String intentId = "pi_expire_test";

    assertThat(shortTtlService.recordIfAbsent(intentId)).isTrue();
    assertThat(shortTtlService.isProcessed(intentId)).isTrue();

    // Sleep past TTL
    Thread.sleep(80);

    // After TTL has expired, it should no longer be marked processed
    assertThat(shortTtlService.isProcessed(intentId)).isFalse();
    // And should be able to record again
    assertThat(shortTtlService.recordIfAbsent(intentId)).isTrue();
  }

  @Test
  void boundsCapacityWhenReachingMaxCapacity() {
    int maxCapacity = 5;
    IdempotencyService boundedService = new IdempotencyService(Duration.ofDays(30), maxCapacity);

    for (int i = 0; i < 10; i++) {
      boundedService.markProcessed("pi_bounded_" + i);
    }

    assertThat(boundedService.size()).isLessThanOrEqualTo(maxCapacity);
  }

  @Test
  void concurrentAccessAllowsExactlyOneRecorder() throws InterruptedException {
    int threadCount = 20;
    String intentId = "pi_concurrent_race_test";

    ExecutorService executor = Executors.newFixedThreadPool(threadCount);
    CountDownLatch readyLatch = new CountDownLatch(threadCount);
    CountDownLatch startLatch = new CountDownLatch(1);
    CountDownLatch doneLatch = new CountDownLatch(threadCount);

    AtomicInteger successCount = new AtomicInteger(0);

    for (int i = 0; i < threadCount; i++) {
      executor.submit(
          () -> {
            readyLatch.countDown();
            try {
              startLatch.await();
              if (service.recordIfAbsent(intentId)) {
                successCount.incrementAndGet();
              }
            } catch (InterruptedException e) {
              Thread.currentThread().interrupt();
            } finally {
              doneLatch.countDown();
            }
          });
    }

    readyLatch.await();
    startLatch.countDown();
    doneLatch.await();
    executor.shutdown();

    assertThat(successCount.get()).isEqualTo(1);
    assertThat(service.isProcessed(intentId)).isTrue();
  }
}
