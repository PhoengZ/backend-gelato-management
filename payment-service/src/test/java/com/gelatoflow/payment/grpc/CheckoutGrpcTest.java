package com.gelatoflow.payment.grpc;

import static com.gelatoflow.payment.testsupport.CheckoutFixtures.*;
import static com.gelatoflow.payment.testsupport.Fixtures.*;
import static org.assertj.core.api.Assertions.*;
import static org.mockito.Mockito.*;

import com.gelatoflow.contracts.payment.v1.*;
import com.gelatoflow.payment.client.stripe.CheckoutPort;
import com.google.protobuf.Timestamp;
import io.grpc.*;
import io.grpc.stub.MetadataUtils;
import java.time.Instant;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.*;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.boot.test.context.SpringBootTest;
import org.springframework.test.context.bean.override.mockito.MockitoBean;

@SpringBootTest(
    properties = {
      "payment.stripe.secret-key=sk_test_fixture",
      "payment.stripe.webhook-secret=whsec_test_fixture",
      "payment.order.service-token=fixture",
      "payment.order.tls=false",
      "payment.jwt.secret=test-signing-secret-at-least-32-bytes",
      "payment.checkout.enabled=true",
      "payment.checkout.port=0",
      "payment.checkout.tls=false",
      "payment.checkout.request-token=" + TOKEN,
      "payment.checkout.success-url=http://localhost:3000/success?session_id={CHECKOUT_SESSION_ID}",
      "payment.checkout.cancel-url=http://localhost:3000/cancel",
      "payment.order-callback.service-token=payment-to-order-test-token-at-least-32-bytes",
      "payment.order-callback.allow-plaintext=true"
    })
class CheckoutGrpcTest {
  @Autowired CheckoutGrpcServer server;
  @MockitoBean CheckoutPort stripe;
  ManagedChannel channel;

  @BeforeEach
  void setup() {
    channel = ManagedChannelBuilder.forAddress("127.0.0.1", server.port()).usePlaintext().build();
    when(stripe.create(any())).thenAnswer(call -> session(call.getArgument(0)));
  }

  @AfterEach
  void cleanup() throws InterruptedException {
    channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
  }

  PaymentServiceGrpc.PaymentServiceBlockingStub client(String credential) {
    var stub = PaymentServiceGrpc.newBlockingStub(channel).withDeadlineAfter(2, TimeUnit.SECONDS);
    if (credential == null) return stub;
    var headers = new Metadata();
    headers.put(Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER), credential);
    return stub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(headers));
  }

  void assertCode(Status.Code expected, Runnable operation) {
    assertThatThrownBy(operation::run)
        .isInstanceOfSatisfying(
            StatusRuntimeException.class,
            error -> assertThat(error.getStatus().getCode()).isEqualTo(expected));
  }

  @Test
  void actualListenerAcceptsOrderTokenAndCanonicalRequestWithoutCallingOrder() {
    var request = request();
    var response = client("Bearer " + TOKEN).createCheckoutSession(request);
    assertThat(response.getCheckoutSessionId()).isEqualTo("cs_test_fixture");
    assertThat(response.getCheckoutUrl()).isEqualTo(URL);
    verify(stripe).create(request);
    assertThat(server.isRunning()).isTrue();
  }

  @Test
  void missingWrongCustomerAndReverseDirectionCredentialsFailBeforeStripe() {
    for (var credential :
        new String[] {
          null, "Bearer wrong", "Bearer customer.jwt.signature", "Bearer fixture", "Basic " + TOKEN
        }) {
      assertCode(
          Status.Code.UNAUTHENTICATED, () -> client(credential).createCheckoutSession(request()));
    }
    verifyNoInteractions(stripe);
  }

  @Test
  void invalidUuidMoneyCurrencyAndTimestampFailBeforeStripe() {
    var request = request();
    for (var invalid :
        new CreateCheckoutSessionRequest[] {
          request.toBuilder().setOrderId("bad").build(),
          request.toBuilder().setCustomerId("00000000-0000-0000-0000-000000000000").build(),
          request.toBuilder().setIdempotencyKey("not-a-key").build(),
          request.toBuilder().setAmountMinor(0).build(),
          request.toBuilder().setAmountMinor(100000000).build(),
          request.toBuilder().setCurrency("thb").build(),
          request.toBuilder().setCurrency("ZZZ").build(),
          request.toBuilder().clearReservationExpiresAt().build(),
          request.toBuilder().setReservationExpiresAt(Timestamp.newBuilder().setNanos(-1)).build()
        }) {
      assertCode(
          Status.Code.INVALID_ARGUMENT,
          () -> client("Bearer " + TOKEN).createCheckoutSession(invalid));
    }
    verifyNoInteractions(stripe);
  }

  @Test
  void expiredReservationIsRejectedBeforeProviderMutation() {
    var expired =
        request().toBuilder()
            .setReservationExpiresAt(
                Timestamp.newBuilder().setSeconds(Instant.now().minusSeconds(1).getEpochSecond()))
            .build();
    assertCode(
        Status.Code.FAILED_PRECONDITION,
        () -> client("Bearer " + TOKEN).createCheckoutSession(expired));
    verifyNoInteractions(stripe);
  }

  @Test
  void providerMismatchesAndUnsafeUrlsAreNeverExposed() {
    var request = request();
    var wrong = session(request);
    wrong.setAmountTotal(1L);
    doReturn(wrong).when(stripe).create(any());
    assertCode(
        Status.Code.UNAVAILABLE, () -> client("Bearer " + TOKEN).createCheckoutSession(request));
    wrong.setAmountTotal(request.getAmountMinor());
    wrong.setUrl("https://checkout.stripe.com.attacker.example/pay");
    assertCode(
        Status.Code.UNAVAILABLE, () -> client("Bearer " + TOKEN).createCheckoutSession(request));
    wrong.setUrl(URL);
    wrong.setLivemode(true);
    assertCode(
        Status.Code.UNAVAILABLE, () -> client("Bearer " + TOKEN).createCheckoutSession(request));
  }

  @Test
  void completedSessionIsRejectedAndProviderStatusesArePreserved() {
    var request = request();
    var completed = session(request);
    completed.setStatus("complete");
    doReturn(completed).when(stripe).create(any());
    assertCode(
        Status.Code.FAILED_PRECONDITION,
        () -> client("Bearer " + TOKEN).createCheckoutSession(request));
    doThrow(Status.ALREADY_EXISTS.withDescription("conflict").asRuntimeException())
        .when(stripe)
        .create(any());
    assertCode(
        Status.Code.ALREADY_EXISTS, () -> client("Bearer " + TOKEN).createCheckoutSession(request));
    doThrow(Status.UNAVAILABLE.asRuntimeException()).when(stripe).create(any());
    assertCode(
        Status.Code.UNAVAILABLE, () -> client("Bearer " + TOKEN).createCheckoutSession(request));
  }

  @Test
  void unexpectedExceptionsDoNotLeakProviderSecrets() {
    doThrow(new IllegalStateException("sk_test_secret " + URL)).when(stripe).create(any());
    assertThatThrownBy(() -> client("Bearer " + TOKEN).createCheckoutSession(request()))
        .isInstanceOfSatisfying(
            StatusRuntimeException.class,
            error -> {
              assertThat(error.getStatus().getCode()).isEqualTo(Status.Code.INTERNAL);
              assertThat(error.getMessage()).doesNotContain("sk_test_secret", URL);
            });
  }

  @Test
  void expiredDuringProviderCallNeverReturnsAUrl() {
    var now = Instant.parse("2030-01-01T00:00:00Z");
    var clock = mock(java.time.Clock.class);
    when(clock.instant()).thenReturn(now);
    var checkout = new com.gelatoflow.payment.service.CheckoutService(stripe, properties(), clock);
    doAnswer(
            call -> {
              when(clock.instant()).thenReturn(now.plusSeconds(601));
              return session(call.getArgument(0));
            })
        .when(stripe)
        .create(any());
    var request = request();
    var deadline = now.plusSeconds(600);
    var expiring =
        request.toBuilder()
            .setReservationExpiresAt(
                Timestamp.newBuilder()
                    .setSeconds(deadline.getEpochSecond())
                    .setNanos(deadline.getNano()))
            .build();
    assertCode(Status.Code.FAILED_PRECONDITION, () -> checkout.create(expiring));
    verify(stripe).create(expiring);
  }

  @Test
  void clientCancellationCanBeReplayedWithTheOriginalFacts() throws Exception {
    var started = new java.util.concurrent.CountDownLatch(1);
    var finish = new java.util.concurrent.CountDownLatch(1);
    doAnswer(
            call -> {
              started.countDown();
              finish.await(2, TimeUnit.SECONDS);
              return session(call.getArgument(0));
            })
        .when(stripe)
        .create(any());
    var headers = new Metadata();
    headers.put(
        Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER), "Bearer " + TOKEN);
    var request = request();
    var pending =
        PaymentServiceGrpc.newFutureStub(channel)
            .withInterceptors(MetadataUtils.newAttachHeadersInterceptor(headers))
            .createCheckoutSession(request);
    try {
      assertThat(started.await(2, TimeUnit.SECONDS)).isTrue();
      assertThat(pending.cancel(true)).isTrue();
    } finally {
      finish.countDown();
    }
    assertThat(client("Bearer " + TOKEN).createCheckoutSession(request).getCheckoutUrl())
        .isEqualTo(URL);
  }
}
