.PHONY: clean
clean: clean-bundle
	$(Q)-rm -rf ${V_FLAG} $(OUT_DIR) ./vendor
	$(Q)go clean ${X_FLAG} ./...

.PHONY: clean-bundle
clean-bundle:
	rm -rf bundle/