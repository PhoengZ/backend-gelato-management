FROM maven:3.9.11-eclipse-temurin-21 AS build
WORKDIR /workspace
COPY contracts/proto contracts/proto
COPY payment-service/pom.xml payment-service/pom.xml
WORKDIR /workspace/payment-service
RUN mvn -B dependency:go-offline
COPY payment-service/src src
RUN mvn -B verify

FROM eclipse-temurin:21-jre-alpine
RUN addgroup -S gelato && adduser -S payment -G gelato
WORKDIR /app
COPY --from=build --chown=payment:gelato /workspace/payment-service/target/payment-service-1.0.0-SNAPSHOT.jar app.jar
USER payment
EXPOSE 3007 50057
HEALTHCHECK --interval=30s --timeout=5s --start-period=45s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:3007/actuator/health/liveness || exit 1
ENTRYPOINT ["java", "-jar", "app.jar"]
