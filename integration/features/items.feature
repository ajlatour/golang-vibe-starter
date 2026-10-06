Feature: Items
  The API serves /items from its upstream, which in this stack is WireMock.

  WireMock doesn't store anything: it always lists the same three items
  (widget, gadget, gizmo), a create always answers with the ID "new-item",
  and nothing written can be read back. So these scenarios check each call
  on its own, never a create followed by a read.

  @smoke
  Scenario: Listing returns the upstream's items, oldest first
    When I GET "/items"
    Then the response status is 200
    And the JSON array "items" has 3 elements
    And the JSON field "items.0.id" is "widget"
    And the JSON field "items.2.id" is "gizmo"

  Scenario: Getting one item
    When I GET "/items/gadget"
    Then the response status is 200
    And the JSON field "id" is "gadget"
    And the JSON field "name" is "Gadget"

  Scenario: An item without a description doesn't send one
    When I GET "/items/gizmo"
    Then the response status is 200
    And the JSON field "description" is absent

  Scenario: Getting an unknown item
    When I GET "/items/no-such-item"
    Then the response status is 404
    And the JSON field "error" is "item not found"

  # An ID with anything but letters, digits, - and _ can't name an item. The
  # API answers 404 itself and never sends it upstream, where a proxy might
  # normalize or decode it into another route.
  Scenario Outline: An ID no item can have is not found
    When I GET "<path>"
    Then the response status is 404
    And the JSON field "error" is "item not found"

    Examples:
      | path                       |
      | /items/%2E%2E              |
      | /items/..%2F..%2F__admin   |
      | /items/a%2Fb               |
      | /items/x%3Fq=1             |
      | /items/with%20space        |

  Scenario: Creating an item
    When I POST "/items" with body:
      """
      {"name": "  Sprocket  ", "description": "Spins."}
      """
    Then the response status is 201
    And the response header "Location" is "/items/new-item"
    And the JSON field "id" is "new-item"
    And the JSON field "name" is "Sprocket"
    And the JSON field "description" is "Spins."

  Scenario: Replacing an item
    When I PUT "/items/widget" with body:
      """
      {"name": "Renamed widget"}
      """
    Then the response status is 200
    And the JSON field "id" is "widget"
    And the JSON field "name" is "Renamed widget"

  Scenario: Replacing an unknown item
    When I PUT "/items/no-such-item" with body:
      """
      {"name": "Anything"}
      """
    Then the response status is 404
    And the JSON field "error" is "item not found"

  Scenario: Deleting an item
    When I DELETE "/items/gizmo"
    Then the response status is 204
    And the response body is empty

  Scenario: Deleting an unknown item
    When I DELETE "/items/no-such-item"
    Then the response status is 404
    And the JSON field "error" is "item not found"

  Scenario: The list comes a page at a time
    When I GET "/items?limit=2"
    Then the response status is 200
    And the JSON array "items" has 2 elements
    And the JSON field "next_cursor" is "after-gadget"

  Scenario: The next page follows the cursor, and the last has none
    When I GET "/items?limit=2&cursor=after-gadget"
    Then the response status is 200
    And the JSON array "items" has 1 element
    And the JSON field "next_cursor" is absent

  Scenario: A cursor the list didn't give out is refused
    When I GET "/items?cursor=made-up"
    Then the response status is 400
    And the JSON field "code" is "invalid_parameter"

  Scenario: A limit out of range is refused, before the upstream is asked
    When I GET "/items?limit=0"
    Then the response status is 400
    And the JSON field "code" is "invalid_parameter"

  Scenario: A write with a stale ETag is refused, and changes nothing
    Given I send the header "If-Match" with value "stale"
    When I PUT "/items/widget" with body:
      """
      {"name": "Lost update"}
      """
    Then the response status is 412
    And the JSON field "code" is "precondition_failed"

  Scenario: Asking for a read that hasn't changed gets a 304
    Given I send the header "If-None-Match" with value "*"
    When I GET "/items/widget"
    Then the response status is 304
    And the response body is empty
