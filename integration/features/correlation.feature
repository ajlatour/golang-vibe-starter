Feature: Correlation IDs
  Every response carries a Client-Correlation-Id header, which is also the
  request_id on the service's log lines for that request. A caller can send
  its own to tie the service's logs to theirs; otherwise, or if the one sent
  isn't valid, the service generates one. The header's name and format are
  read from docs/openapi.json, so these scenarios hold the spec and the
  service to each other.

  Scenario: A caller's own ID is echoed back
    Given I send the correlation ID "checkout-7f3a:attempt.2"
    When I GET "/items"
    Then the response status is 200
    And the correlation ID I sent is echoed back

  Scenario: Without one, the service generates an ID
    When I GET "/items"
    Then the response has a generated correlation ID

  Scenario: Each request gets its own generated ID
    When I GET "/healthz"
    And I GET "/healthz"
    Then the last two responses have different correlation IDs

  Scenario Outline: An ID that isn't valid is replaced
    Given I send the correlation ID "<id>"
    When I GET "/items"
    Then the response status is 200
    And the response has a generated correlation ID

    Examples:
      | id              | why                       |
      | has spaces      | a space                   |
      | a/b             | a slash                   |
      | café            | a non-ASCII letter        |

  Scenario: An ID with a quote in it is replaced
    A quote is how a value could break out of a log field, so it matters most.

    Given I send a correlation ID with a quote in it
    When I GET "/items"
    Then the response has a generated correlation ID

  Scenario: An ID at the length limit is kept
    Given I send a correlation ID of 128 characters
    When I GET "/healthz"
    Then the correlation ID I sent is echoed back

  Scenario: An ID over the length limit is replaced
    Given I send a correlation ID of 129 characters
    When I GET "/healthz"
    Then the response has a generated correlation ID

  Scenario Outline: Error responses carry the ID too
    So a caller can quote it when reporting the failure.

    Given I send the correlation ID "report-me-42"
    When I <method> "<path>"
    Then the response status is 404
    And the correlation ID I sent is echoed back

    Examples:
      | method | path              |
      | GET    | /items/no-such-id |
      | DELETE | /items/no-such-id |

  Scenario: An upstream failure carries the ID too
    Given I send the correlation ID "report-me-502"
    And the upstream fault "unavailable" is active
    When I GET "/items"
    Then the response status is 502
    And the correlation ID I sent is echoed back

  Scenario: A refused request body carries the ID too
    Given I send the correlation ID "report-me-43"
    When I POST "/items" with body:
      """
      {"name": ""}
      """
    Then the response status is 422
    And the correlation ID I sent is echoed back
