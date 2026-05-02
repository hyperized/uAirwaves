General
1. Follow the principle of least privilege when accessing resources.
2. Use code reviews to ensure code quality and maintainability.
3. Use automated testing to ensure code quality and maintainability.
4. Use static analysis tools to catch potential issues before they become problems.
5. When writing code, always follow the coding standards and conventions of the programming language being used.
6. Ensure the code is idiomatic and follows best practices for the programming language.
7. Ensure output is lint-free and formatted correctly.
8. Test code thoroughly to ensure it works as expected and handles edge cases.
9. Keep code organized and modular to enhance maintainability.
10. Ensure code is optimized for performance and scalability.
11. Ensure code can be benchmarked and optimized for performance.
12. All your code should be thread safe
13. Keep the comments to the minimum necessary for understanding and maintenance.
14. Avoid unnecessary complexity and duplication to keep the codebase clean and easy to understand.
15. Follow the SOLID principles to ensure maintainable and scalable code.
16. Stay consistent, if you change the code, ensure it remains consistent with the rest of the codebase.
17. Use meaningful and descriptive variable names to improve code readability.

Testing Information
1. Tests should cover all cases
2. Package tests (inside `pkg`) should be split up in internal and external tests
3. Internal should cover all private methods and side effects that can only be tested with internal access
4. External should cover public methods and their interactions with the system
5. Tests should be written in a way that they can be run in parallel without side effects
6. Test helper functions should contain the t.Helper annotation to avoid polluting the test output with helper function names
7. Code coverage should be 100%.
8. You cannot remove testing code only to pass the tests.
9. Remove all test artifacts (like coverage files) after use.

Linting and formatting information
1. Use golangci-lint with the provided .golangci.yaml configuration file
2. Use gofmt to format code
3. Use golangci-lint to format the code
4. Use goimports to format imports

Performance
1. Reduce dependencies on third parties where possible
2. If there's a potential for a faster implementation, provide benchmarks and compare to pick the fastest option without losing functionality
3. Follow the mantra: Share Memory By Communicating
4. Where possible, always use the most optimal choice of a data-type to reduce memory constraints
5. Avoid unnecessary allocations and use efficient data structures.
6. Avoid using reflection where possible, as it can be slow and can lead to runtime errors.
7. Use type assertions judiciously, as they can lead to runtime errors if the type assertion fails.
8. Use interfaces to decouple dependencies and make code more testable and maintainable.
9. Use channels to communicate between goroutines, as they are more efficient than using mutexes and condition variables.
10. Use context to manage cancellation and timeouts in concurrent operations.
11. Use select statements to handle multiple channels efficiently.
12. Use atomic operations to synchronize access to shared memory.
13. Use buffered channels judiciously, as they can lead to memory leaks if not managed properly.
14. Use defer statements judiciously, as they can lead to memory leaks if not managed properly.
15. Use error handling to gracefully handle errors and prevent program crashes. 
16. Use panic and recover judiciously, as they can lead to program crashes if not managed properly.

Logging
1. Use logging to track the execution of the program and debug issues.
2. Use the slog/log package for structured logging.
3. Use structured logging to make it easier to analyze logs and debug issues.
4. Use log levels to categorize log messages and make it easier to filter logs.
5. Use log formatting to make it easier to read and analyze logs.

Self improvement
1. If you are given an instruction to improve, add it to this file.
2. Don't offer suggestions that violate all of the above.
3. Highlight it if you spot any inconsistencies in this file.

Tone
1. Use a professional tone, assume the user is competent
2. Don't use encouraging language

