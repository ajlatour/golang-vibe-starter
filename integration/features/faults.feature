Feature: Upstream faults
  When the upstream fails, the API answers 502 Bad Gateway, or 504 Gateway
  Timeout if it didn't answer in time, with only a generic message: never a
  500, which would mean the API itself broke. Each scenario switches on one of
  WireMock's fault stubs (wiremock/faults/) through its admin API.

  Scenario Outline: The upstream can't give a usable answer
    Given the upstream fault "<fault>" is active
    When I <method> "<path>"
    Then the response status is 502
    And the JSON field "error" is "bad gateway"

    Examples:
      | fault            | method | path          | what the upstream does         |
      | unavailable      | GET    | /items        | answers 503                    |
      | unavailable      | GET    | /items/widget | answers 503 on every route     |
      | unavailable      | DELETE | /items/widget | answers 503 to writes as well  |
      | connection-reset | GET    | /items        | resets the connection          |
      | connection-reset | GET    | /items/gadget | resets it on every route       |
      | malformed-list   | GET    | /items        | sends truncated JSON           |

  Scenario: A write while the upstream is unavailable
    Given the upstream fault "unavailable" is active
    When I POST "/items" with body:
      """
      {"name": "Sprocket"}
      """
    Then the response status is 502
    And the JSON field "error" is "bad gateway"

  Scenario: The upstream doesn't answer in time
    The compose override sets UPSTREAM_TIMEOUT to 3s, and this fault delays the
    upstream's answer by 8s, so the API gives up at 3s.

    Given the upstream fault "timeout-list" is active
    When I GET "/items"
    Then the response status is 504
    And the JSON field "error" is "gateway timeout"
    And the response took at least 3 seconds
    And the response took less than 5 seconds

  Scenario: A slow upstream within the timeout is not a failure
    The slow stub lists a single item, unlike the normal stub's three, which
    shows the answer really came through the delayed path.

    Given the upstream fault "slow-list" is active
    When I GET "/items"
    Then the response status is 200
    And the response took at least 2 seconds
    And the JSON array "items" has 1 element
    And the JSON field "items.0.id" is "widget"

  Scenario: A fault on one route leaves the others working
    Given the upstream fault "timeout-list" is active
    When I GET "/items/widget"
    Then the response status is 200
    And the JSON field "id" is "widget"

  Scenario: A GET the upstream fails once is retried, and succeeds
    Given the upstream fault "flaky-list" is active
    When I GET "/items"
    Then the response status is 200
    And the JSON array "items" has 3 elements
