FROM alpine:3.6
ADD bin/ratelimit* /bin/
RUN chmod +x "/bin/ratelimit"
CMD ["/bin/ratelimit"]
