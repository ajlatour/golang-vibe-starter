Feature: Request validation
  The API checks a create or replace body itself, before calling the upstream,
  and refuses a bad one: 400 when the body can't be read as an item at all,
  422 when it can but a value is wrong.

  Scenario Outline: A body that can't be read as an item
    When I POST "/items" with body:
      """
      <body>
      """
    Then the response status is 400
    And the JSON field "error" contains "<message>"

    Examples:
      | body                           | message            |
      | {"name":                       | invalid JSON       |
      | {"name": "x", "colour": "red"} | unknown field      |
      | {"name": 1}                    | invalid JSON       |
      | {"name": "x"} {"name": "y"}    | single JSON object |

  Scenario: An empty body
    When I POST "/items" with body:
      """
      """
    Then the response status is 400
    And the JSON field "error" is "request body is empty"

  Scenario Outline: A name that isn't valid
    When I <method> "<path>" with body:
      """
      <body>
      """
    Then the response status is 422
    And the JSON field "error" is "name is required"

    Examples:
      | method | path          | body                  |
      | POST   | /items        | {"name": ""}          |
      | POST   | /items        | {"name": "   "}       |
      | POST   | /items        | {"description": "x"}  |
      | PUT    | /items/widget | {"name": ""}          |

  Scenario: A name at the length limit is accepted
    When I POST "/items" with a name of 200 characters
    Then the response status is 201

  Scenario: A name over the length limit is refused
    When I POST "/items" with a name of 201 characters
    Then the response status is 422
    And the JSON field "error" is "name must be at most 200 characters"
