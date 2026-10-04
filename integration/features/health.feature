@smoke
Feature: Health check
  The container's healthcheck and any load balancer ask /healthz whether the
  service can answer HTTP at all.

  Scenario: The service reports that it is up
    When I GET "/healthz"
    Then the response status is 200
    And the JSON field "status" is "ok"
