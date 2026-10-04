package com.gelatoflow.payment.config;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.gelatoflow.payment.dto.ErrorResponse;
import com.gelatoflow.payment.validation.Ids;
import java.nio.charset.StandardCharsets;
import java.util.List;
import javax.crypto.spec.SecretKeySpec;
import org.springframework.context.annotation.*;
import org.springframework.http.HttpMethod;
import org.springframework.security.config.annotation.web.builders.HttpSecurity;
import org.springframework.security.config.http.SessionCreationPolicy;
import org.springframework.security.core.authority.SimpleGrantedAuthority;
import org.springframework.security.oauth2.core.*;
import org.springframework.security.oauth2.jose.jws.MacAlgorithm;
import org.springframework.security.oauth2.jwt.*;
import org.springframework.security.oauth2.server.resource.authentication.JwtAuthenticationConverter;
import org.springframework.security.web.SecurityFilterChain;

@Configuration
public class SecurityConfig {
  @Bean
  JwtDecoder jwtDecoder(PaymentProperties properties) {
    var config = properties.jwt();
    var decoder =
        NimbusJwtDecoder.withSecretKey(
                new SecretKeySpec(config.secret().getBytes(StandardCharsets.UTF_8), "HmacSHA256"))
            .macAlgorithm(MacAlgorithm.HS256)
            .build();
    OAuth2TokenValidator<Jwt> claims =
        jwt -> {
          boolean valid =
              jwt.getAudience() != null
                  && jwt.getAudience().contains(config.audience())
                  && Ids.isUuid(jwt.getSubject())
                  && jwt.getClaim("role") instanceof String role
                  && List.of("CUSTOMER", "STAFF", "MANAGER").contains(role)
                  && jwt.getExpiresAt() != null
                  && jwt.getIssuedAt() != null
                  && !jwt.getIssuedAt().isAfter(java.time.Instant.now())
                  && jwt.getExpiresAt().isAfter(jwt.getIssuedAt());
          return valid
              ? OAuth2TokenValidatorResult.success()
              : OAuth2TokenValidatorResult.failure(new OAuth2Error("invalid_token"));
        };
    decoder.setJwtValidator(
        new DelegatingOAuth2TokenValidator<>(
            JwtValidators.createDefaultWithIssuer(config.issuer()), claims));
    return decoder;
  }

  @Bean
  SecurityFilterChain security(HttpSecurity http, ObjectMapper mapper) throws Exception {
    var converter = new JwtAuthenticationConverter();
    converter.setJwtGrantedAuthoritiesConverter(
        jwt ->
            "CUSTOMER".equals(jwt.getClaimAsString("role"))
                ? List.of(new SimpleGrantedAuthority("ROLE_CUSTOMER"))
                : List.of());
    return http.csrf(csrf -> csrf.disable())
        .sessionManagement(
            session -> session.sessionCreationPolicy(SessionCreationPolicy.STATELESS))
        .authorizeHttpRequests(
            auth ->
                auth.requestMatchers(HttpMethod.POST, "/api/v1/payments/webhook")
                    .permitAll()
                    .requestMatchers(HttpMethod.GET, "/actuator/health", "/actuator/health/**")
                    .permitAll()
                    .requestMatchers(HttpMethod.POST, "/api/v1/payments/create-payment-intent")
                    .hasRole("CUSTOMER")
                    .anyRequest()
                    .denyAll())
        .oauth2ResourceServer(
            oauth ->
                oauth
                    .jwt(jwt -> jwt.jwtAuthenticationConverter(converter))
                    .authenticationEntryPoint(
                        (request, response, exception) -> {
                          response.setStatus(401);
                          response.setContentType("application/json");
                          mapper.writeValue(
                              response.getOutputStream(),
                              new ErrorResponse("UNAUTHORIZED", "Valid bearer token required"));
                        }))
        .exceptionHandling(
            errors ->
                errors
                    .authenticationEntryPoint(
                        (request, response, exception) -> {
                          response.setStatus(401);
                          response.setContentType("application/json");
                          mapper.writeValue(
                              response.getOutputStream(),
                              new ErrorResponse("UNAUTHORIZED", "Valid bearer token required"));
                        })
                    .accessDeniedHandler(
                        (request, response, exception) -> {
                          response.setStatus(403);
                          response.setContentType("application/json");
                          mapper.writeValue(
                              response.getOutputStream(),
                              new ErrorResponse("FORBIDDEN", "Customer access required"));
                        }))
        .build();
  }
}
