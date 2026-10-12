package com.gelatoflow.payment.client.order;

import static com.gelatoflow.payment.testsupport.Fixtures.*;
import static org.assertj.core.api.Assertions.*;

import com.gelatoflow.contracts.order.v1.*;
import com.gelatoflow.payment.exception.ApiException;
import io.grpc.*;
import io.grpc.inprocess.*;
import io.grpc.stub.StreamObserver;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.Test;
import org.springframework.http.HttpStatus;

class GrpcOrderClientTest {
  @Test
  void sendsMachineCredentialAndDeadlineAndCallsCanonicalRpcs() throws Exception {
    var auth = new AtomicReference<String>();
    var deadline = new AtomicReference<Deadline>();
    var report = new AtomicReference<ReportPaymentResultRequest>();
    var server =
        InProcessServerBuilder.forName("payment-order-test")
            .directExecutor()
            .intercept(
                new ServerInterceptor() {
                  @Override
                  public <ReqT, RespT> ServerCall.Listener<ReqT> interceptCall(
                      ServerCall<ReqT, RespT> call,
                      Metadata headers,
                      ServerCallHandler<ReqT, RespT> next) {
                    auth.set(
                        headers.get(
                            Metadata.Key.of("authorization", Metadata.ASCII_STRING_MARSHALLER)));
                    deadline.set(Context.current().getDeadline());
                    return next.startCall(call, headers);
                  }
                })
            .addService(
                new OrderServiceGrpc.OrderServiceImplBase() {
                  @Override
                  public void getOrderDetails(
                      GetOrderDetailsRequest request,
                      StreamObserver<GetOrderDetailsResponse> response) {
                    response.onNext(order());
                    response.onCompleted();
                  }

                  @Override
                  public void bindPaymentIntent(
                      BindPaymentIntentRequest request,
                      StreamObserver<BindPaymentIntentResponse> response) {
                    response.onNext(
                        BindPaymentIntentResponse.newBuilder()
                            .setPaymentIntentId(request.getPaymentIntentId())
                            .build());
                    response.onCompleted();
                  }

                  @Override
                  public void reportPaymentResult(
                      ReportPaymentResultRequest request,
                      StreamObserver<ReportPaymentResultResponse> response) {
                    report.set(request);
                    response.onNext(ReportPaymentResultResponse.getDefaultInstance());
                    response.onCompleted();
                  }
                })
            .build()
            .start();
    var channel = InProcessChannelBuilder.forName("payment-order-test").directExecutor().build();
    try {
      var client = new GrpcOrderClient(channel, properties());
      assertThat(client.get(ORDER).getCustomerId()).isEqualTo(CUSTOMER);
      client.bind(BindPaymentIntentRequest.newBuilder().setPaymentIntentId("pi_fixture").build());
      client.report(
          ReportPaymentResultRequest.newBuilder().setProviderEventId("evt_fixture").build());
      assertThat(auth.get()).isEqualTo("Bearer test-machine-token");
      assertThat(deadline.get()).isNotNull();
      assertThat(report.get().getProviderEventId()).isEqualTo("evt_fixture");
    } finally {
      channel.shutdownNow();
      server.shutdownNow();
    }
  }

  @Test
  void unavailableOrUnimplementedReportingIsRetryableAndNotFoundIsMapped() throws Exception {
    var name = InProcessServerBuilder.generateName();
    var server =
        InProcessServerBuilder.forName(name)
            .directExecutor()
            .addService(
                new OrderServiceGrpc.OrderServiceImplBase() {
                  @Override
                  public void getOrderDetails(
                      GetOrderDetailsRequest request,
                      StreamObserver<GetOrderDetailsResponse> response) {
                    response.onError(
                        Status.NOT_FOUND.withDescription("private details").asRuntimeException());
                  }
                })
            .build()
            .start();
    var channel = InProcessChannelBuilder.forName(name).directExecutor().build();
    try {
      var client = new GrpcOrderClient(channel, properties());
      assertThatThrownBy(() -> client.get(ORDER))
          .isInstanceOfSatisfying(
              ApiException.class,
              e -> {
                assertThat(e.status()).isEqualTo(HttpStatus.NOT_FOUND);
                assertThat(e.getMessage()).doesNotContain("private details");
              });
      assertThatThrownBy(() -> client.report(ReportPaymentResultRequest.getDefaultInstance()))
          .isInstanceOfSatisfying(
              ApiException.class,
              e -> assertThat(e.status()).isEqualTo(HttpStatus.SERVICE_UNAVAILABLE));
    } finally {
      channel.shutdownNow();
      server.shutdownNow();
    }
  }
}
