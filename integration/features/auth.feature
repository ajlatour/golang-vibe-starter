Feature: API keys
  Every route but the healthcheck needs an API key, sent as
  Authorization: Bearer <key>. The stack's development keys come from
  compose.yaml: gvs_local-development-only may read and write,
  gvs_local-read-only may only read. Every other scenario sends the first.

  Scenario: A request without a key is refused
    Given I send no API key
    When I GET "/items"
    Then the response status is 401
    And the response header "WWW-Authenticate" starts with "Bearer "
    And the JSON field "error" contains "API key is required"

  Scenario: A key the service doesn't know is refused
    Given I use the API key "gvs_not-a-key"
    When I GET "/items"
    Then the response status is 401
    And the JSON field "error" is "the API key is not valid"

  Scenario: A read-only key can read
    Given I use the API key "gvs_local-read-only"
    When I GET "/items/widget"
    Then the response status is 200

  Scenario Outline: A read-only key can't write
    Given I use the API key "gvs_local-read-only"
    When I <method> "<path>" with body:
      """
      {"name": "Sprocket"}
      """
    Then the response status is 403
    And the JSON field "error" is "this API key may only read"

    Examples:
      | method | path          |
      | POST   | /items        |
      | PUT    | /items/widget |

  Scenario: A read-only key can't delete
    Given I use the API key "gvs_local-read-only"
    When I DELETE "/items/widget"
    Then the response status is 403

  Scenario: The healthcheck needs no key
    Given I send no API key
    When I GET "/healthz"
    Then the response status is 200

  Scenario: A refusal still gets a correlation ID
    Given I send no API key
    When I GET "/items"
    Then the response has a generated correlation ID
