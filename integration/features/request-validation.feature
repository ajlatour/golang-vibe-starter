Feature: Request validation
  Every request but the probes is checked against the request-validation
  service (VALIDATOR_URL), here WireMock's stub, which allows every request but
  /items/refused-by-validator. The request-validation-enforce flag decides
  whether its answers are acted on (on) or only logged (off, the default:
  shadow mode). Enforcing, the service fails closed: when the validation
  service can't answer, requests are refused.

  The order matters: an outage, in either mode, makes the API ask the
  validation service again only once a second. So the shadow-mode outage is
  followed straight away by the enforcing one, which ends by waiting until
  requests are checked again; scenarios after it start from a service that
  answers.

  Scenario: A request the validation service allows goes through
    When I GET "/items"
    Then the response status is 200

  Scenario: Enforcing, a request the validation service refuses is a 403, without its reason
    Given the feature flag "request-validation-enforce" is on
    When I GET "/items/refused-by-validator"
    Then the response status is 403
    And the JSON field "error" is "the request was refused"

  Scenario: In shadow mode, a request the validation service refuses goes through
    Given the feature flag "request-validation-enforce" is off
    When I GET "/items/refused-by-validator"
    Then the response status is 404
    And the JSON field "error" is "item not found"

  Scenario: In shadow mode, when the validation service can't answer, requests go through
    Given the feature flag "request-validation-enforce" is off
    And the upstream fault "validator-down" is active
    When I GET "/items"
    Then the response status is 200

  Scenario: Enforcing, when the validation service can't answer, requests are refused
    Given the feature flag "request-validation-enforce" is on
    And the upstream fault "validator-down" is active
    When I GET "/items"
    Then the response status is 503
    And the response header "Retry-After" is "1"
    And the JSON field "error" contains "request validation is unavailable"
    And requests are checked again within 2 seconds of the fault ending

  Scenario: The probes are never checked
    Given the feature flag "request-validation-enforce" is on
    And the upstream fault "validator-down" is active
    When I GET "/healthz"
    Then the response status is 200
