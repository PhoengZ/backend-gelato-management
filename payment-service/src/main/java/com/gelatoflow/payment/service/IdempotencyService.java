package com.gelatoflow.payment.service;

import java.time.Duration;
import java.time.Instant;
import java.util.Iterator;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.stereotype.Service;

/**
 * In-memory bounded cache for processed PaymentIntent IDs with a 30-day retention window. Guards
 * against duplicate webhooks after Stripe's 24-hour idempotency key pruning window.
 */
@Service
public class IdempotencyService {
  private static final Logger log = LoggerFactory.getLogger(IdempotencyService.class);

  public static final Duration DEFAULT_TTL = Duration.ofDays(30);
  public static final int DEFAULT_MAX_CAPACITY = 100_000;

  private final Duration ttl;
  private final int maxCapacity;
  private final ConcurrentHashMap<String, Instant> processedIntents = new ConcurrentHashMap<>();

  public IdempotencyService() {
    this(DEFAULT_TTL, DEFAULT_MAX_CAPACITY);
  }

  public IdempotencyService(Duration ttl, int maxCapacity) {
    if (ttl == null || ttl.isNegative() || ttl.isZero()) {
      throw new IllegalArgumentException("TTL must be positive");
    }
    if (maxCapacity <= 0) {
      throw new IllegalArgumentException("maxCapacity must be greater than zero");
    }
    this.ttl = ttl;
    this.maxCapacity = maxCapacity;
  }

  /**
   * Attempts to record the PaymentIntent ID as processed atomically.
   *
   * @param paymentIntentId Stripe PaymentIntent ID
   * @return {@code true} if newly recorded (first time processed), {@code false} if already
   *     processed within the 30-day TTL window (duplicate)
   */
  public boolean recordIfAbsent(String paymentIntentId) {
    if (paymentIntentId == null || paymentIntentId.isBlank()) {
      return false;
    }

    cleanUpExpired();
    Instant now = Instant.now();
    final boolean[] recorded = new boolean[1];

    processedIntents.compute(
        paymentIntentId,
        (key, existing) -> {
          if (existing == null || isExpired(existing, now)) {
            recorded[0] = true;
            return now;
          }
          recorded[0] = false;
          return existing;
        });

    if (recorded[0]) {
      ensureCapacity();
    } else {
      log.info(
          "PaymentIntent {} already processed within TTL window; rejecting duplicate",
          paymentIntentId);
    }
    return recorded[0];
  }

  /**
   * Explicitly marks a PaymentIntent ID as processed with current timestamp.
   *
   * @param paymentIntentId Stripe PaymentIntent ID
   */
  public void markProcessed(String paymentIntentId) {
    if (paymentIntentId == null || paymentIntentId.isBlank()) {
      return;
    }
    cleanUpExpired();
    processedIntents.put(paymentIntentId, Instant.now());
    ensureCapacity();
  }

  /**
   * Checks whether the given PaymentIntent ID has already been processed within the TTL window.
   *
   * @param paymentIntentId Stripe PaymentIntent ID
   * @return {@code true} if processed and not expired, {@code false} otherwise
   */
  public boolean isProcessed(String paymentIntentId) {
    if (paymentIntentId == null || paymentIntentId.isBlank()) {
      return false;
    }
    Instant timestamp = processedIntents.get(paymentIntentId);
    if (timestamp == null) {
      return false;
    }
    if (isExpired(timestamp, Instant.now())) {
      processedIntents.remove(paymentIntentId, timestamp);
      return false;
    }
    return true;
  }

  /**
   * Alias for {@link #isProcessed(String)}.
   *
   * @param paymentIntentId Stripe PaymentIntent ID
   * @return {@code true} if already processed, {@code false} otherwise
   */
  public boolean isDuplicate(String paymentIntentId) {
    return isProcessed(paymentIntentId);
  }

  /** Returns the count of active, unexpired entries in the cache. */
  public int size() {
    cleanUpExpired();
    return processedIntents.size();
  }

  /** Clears all cached entries. */
  public void clear() {
    processedIntents.clear();
  }

  private boolean isExpired(Instant recordedAt, Instant now) {
    return recordedAt.plus(ttl).isBefore(now);
  }

  private void cleanUpExpired() {
    Instant now = Instant.now();
    processedIntents.entrySet().removeIf(entry -> isExpired(entry.getValue(), now));
  }

  private synchronized void ensureCapacity() {
    if (processedIntents.size() >= maxCapacity) {
      cleanUpExpired();
      if (processedIntents.size() >= maxCapacity) {
        Iterator<Map.Entry<String, Instant>> it = processedIntents.entrySet().iterator();
        int toRemove = Math.max(1, maxCapacity / 10);
        while (it.hasNext() && toRemove > 0) {
          it.next();
          it.remove();
          toRemove--;
        }
      }
    }
  }
}
