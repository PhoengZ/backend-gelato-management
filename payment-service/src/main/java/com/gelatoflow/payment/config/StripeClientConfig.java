package com.gelatoflow.payment.config;

import com.stripe.StripeClient;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
public class StripeClientConfig {
  @Bean
  StripeClient stripeClient(PaymentProperties properties) {
    return StripeClient.builder()
        .setApiKey(properties.stripe().secretKey())
        .setConnectTimeout(5000)
        .setReadTimeout(15000)
        .setMaxNetworkRetries(1)
        .build();
  }
}
