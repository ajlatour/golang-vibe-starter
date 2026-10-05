@smoke
Feature: Health and readiness
  Two probes. /healthz says whether the service can answer HTTP at all: the
  container's healthcheck restarts it if not. /readyz says whether to send
  it traffic: only while it can reach the upstream, and not once it starts
  shutting down. Neither needs an API key.

  Scenario: The service reports that it is up
    When I GET "/healthz"
    Then the response status is 200
    And the JSON field "status" is "ok"

  Scenario: The service reports that it is ready
    Given I send no API key
    When I GET "/readyz"
    Then the response status is 200
    And the JSON field "status" is "ready"

  # Readiness asks whether the upstream can be reached, not whether it's
  # well: an upstream that answers with errors still counts, so a struggling
  # upstream doesn't take every instance out of service at once.
  Scenario: An upstream answering with errors still counts as reachable
    Given the upstream fault "unavailable" is active
    When I GET "/readyz"
    Then the response status is 200
