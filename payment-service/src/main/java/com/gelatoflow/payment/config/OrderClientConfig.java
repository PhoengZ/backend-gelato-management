package com.gelatoflow.payment.config;

import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
public class OrderClientConfig {
  @Bean(destroyMethod = "shutdownNow")
  ManagedChannel orderChannel(PaymentProperties properties) {
    var builder = ManagedChannelBuilder.forTarget(properties.order().target());
    if (properties.order().tls()) builder.useTransportSecurity();
    else builder.usePlaintext();
    return builder.build();
  }
}
