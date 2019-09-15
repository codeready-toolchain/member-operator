.PHONY: clean
clean:
	$(Q)-rm -rf ${V_FLAG} $(OUT_DIR) ./vendor
	$(Q)go clean ${X_FLAG} ./...
	$(Q)-rm -f ${V_FLAG} ./templates/bindata.go