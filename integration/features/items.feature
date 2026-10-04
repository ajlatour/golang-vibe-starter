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
