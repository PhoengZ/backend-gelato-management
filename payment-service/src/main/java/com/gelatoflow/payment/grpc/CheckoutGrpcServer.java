package com.gelatoflow.payment.grpc;

import com.gelatoflow.payment.config.CheckoutProperties;
import io.grpc.Server;
import io.grpc.ServerInterceptors;
import io.grpc.netty.shaded.io.grpc.netty.NettyServerBuilder;
import java.io.File;
import java.io.IOException;
import java.util.concurrent.TimeUnit;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.SmartLifecycle;
import org.springframework.stereotype.Component;

@Component
@ConditionalOnProperty(
    name = "payment.checkout.enabled",
    havingValue = "true",
    matchIfMissing = true)
public class CheckoutGrpcServer implements SmartLifecycle {
  private final Server server;
  private volatile boolean running;

  public CheckoutGrpcServer(CheckoutProperties properties, CheckoutGrpcService service) {
    var builder =
        NettyServerBuilder.forPort(properties.port())
            .maxInboundMessageSize(16384)
            .addService(
                ServerInterceptors.intercept(
                    service, new OrderAuthenticationInterceptor(properties.requestToken())));
    if (properties.tls()) {
      builder.useTransportSecurity(
          new File(properties.certificate()), new File(properties.privateKey()));
    }
    server = builder.build();
  }

  @Override
  public void start() {
    try {
      server.start();
      running = true;
    } catch (IOException e) {
      throw new IllegalStateException("Payment checkout gRPC server could not start", e);
    }
  }

  @Override
  public void stop() {
    server.shutdown();
    try {
      if (!server.awaitTermination(5, TimeUnit.SECONDS)) server.shutdownNow();
    } catch (InterruptedException e) {
      server.shutdownNow();
      Thread.currentThread().interrupt();
    } finally {
      running = false;
    }
  }

  public int port() {
    return server.getPort();
  }

  @Override
  public boolean isRunning() {
    return running;
  }
}
