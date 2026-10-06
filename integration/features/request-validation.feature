Feature: Request validation
  Every request but the probes is checked against the request-validation
  service (VALIDATOR_URL), here WireMock's stub, which allows every request but
  /items/refused-by-validator. The service fails closed: when the validation
  service can't answer, requests are refused.

  Scenario: A request the validation service allows goes through
    When I GET "/items"
    Then the response status is 200

  Scenario: A request the validation service refuses is a 403, without its reason
    When I GET "/items/refused-by-validator"
    Then the response status is 403
    And the JSON field "error" is "the request was refused"

  Scenario: When the validation service can't answer, requests are refused
    Given the upstream fault "validator-down" is active
    When I GET "/items"
    Then the response status is 503
    And the response header "Retry-After" is "1"
    And the JSON field "error" contains "request validation is unavailable"
    And requests are checked again within 2 seconds of the fault ending

  Scenario: The probes are never checked
    Given the upstream fault "validator-down" is active
    When I GET "/healthz"
    Then the response status is 200
