Feature: Request header screening
  Every request's headers are screened before the handlers see them
  (internal/headerguard): a request with a body must send it as JSON, headers
  have size limits, some may appear only once, and proxy and method-override
  headers are stripped. A rejected request still gets a JSON error and a
  correlation ID.

  Scenario Outline: A body that isn't sent as JSON is refused
    When I POST "/items" as "<type>" with body:
      """
      {"name": "Sprocket"}
      """
    Then the response status is 415
    And the JSON field "error" contains "application/json"
    And the response has a generated correlation ID

    Examples:
      | type            |
      | text/plain      |
      | application/xml |

  Scenario: A body without a Content-Type is refused
    When I POST "/items" without a content type, with body:
      """
      {"name": "Sprocket"}
      """
    Then the response status is 415

  Scenario: JSON with a charset is accepted
    When I POST "/items" as "application/json; charset=utf-8" with body:
      """
      {"name": "Sprocket"}
      """
    Then the response status is 201

  Scenario: A header over 8 KiB is refused
    Given I send a header "X-Big" of 9000 bytes
    When I GET "/items"
    Then the response status is 431
    And the JSON field "error" contains "X-Big"

  Scenario: Too many headers are refused, even by the health check
    Given I send 101 extra headers
    When I GET "/healthz"
    Then the response status is 431

  Scenario: A correlation ID sent twice is refused
    Given I send the header "Client-Correlation-Id" with value "first"
    And I send the header "Client-Correlation-Id" with value "second"
    When I GET "/items"
    Then the response status is 400
    And the JSON field "error" contains "Client-Correlation-Id"

  Scenario: A method override header is ignored
    Clients can't turn a GET into a DELETE: the override header is stripped
    before the handlers run.

    Given I send the header "X-HTTP-Method-Override" with value "DELETE"
    When I GET "/items/widget"
    Then the response status is 200
    And the JSON field "id" is "widget"
