WEBCONSOLE_SRC_DIR = $(PWD)/webconsole_source
DIST_DIR = $(WEBCONSOLE_SRC_DIR)/dist
TARGET_DIR = $(PWD)/pkg/consoleplugin/contentserver/static

.PHONY: generate-webconsole-scripts
generate-webconsole-scripts:
	mkdir -p $(DIST_DIR)
	rm -f $(DIST_DIR)/*
	cd $(WEBCONSOLE_SRC_DIR);${IMAGE_BUILDER} build --no-cache --volume $(DIST_DIR):/dist:z,U -f $(WEBCONSOLE_SRC_DIR)/Containerfile .
	rm -f $(TARGET_DIR)/*
	cp $(DIST_DIR)/* $(TARGET_DIR)/
