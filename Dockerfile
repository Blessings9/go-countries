FROM alpine:3.14

# Set working directory
WORKDIR /app

# Copy the Go binary from the workflow build context into the container
COPY go-countries .

# Make the binary executable
RUN chmod +x ./go-countries

# Define the command to run the app
CMD ["/app/go-countries"]

