.PHONY: clean
clean:
	$(Q)-rm -rf ${V_FLAG} $(OUT_DIR)
	$(Q)go clean ${X_FLAG} ./...