-- SPDX-License-Identifier: MPL-2.0
-- Additive admission profile; retained v1 rows and asset bytes are unchanged.
ALTER TABLE media.assets DROP CONSTRAINT assets_mime_check;
ALTER TABLE media.assets DROP CONSTRAINT assets_check1;
ALTER TABLE media.assets ADD CONSTRAINT assets_mime_check CHECK (
 mime IN ('','image/png',
 'image/jpeg',
 'image/gif',
 'image/webp',
 'image/avif',
 'image/bmp',
 'image/tiff',
 'image/x-icon',
 'application/pdf',
 'application/msword',
 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
 'application/vnd.ms-excel',
 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
 'application/vnd.ms-powerpoint',
 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
 'application/vnd.oasis.opendocument.text',
 'application/vnd.oasis.opendocument.spreadsheet',
 'application/vnd.oasis.opendocument.presentation',
 'text/plain',
 'text/csv',
 'application/zip',
 'application/vnd.rar',
 'application/x-7z-compressed',
 'video/mp4',
 'video/quicktime',
 'video/webm',
 'video/matroska',
 'video/x-msvideo',
 'video/mpeg',
 'video/ogg',
 'audio/mpeg',
 'audio/mp4',
 'audio/ogg',
 'audio/wav',
 'audio/flac',
 'audio/aac')
);
ALTER TABLE media.assets ADD CONSTRAINT assets_dimensions_check CHECK (
 (mime IN ('image/png','image/jpeg')) OR (width=0 AND height=0)
);
ALTER TABLE media.assets ADD CONSTRAINT assets_ready_check CHECK (
 state <> 'ready' OR (
  mime <> '' AND size > 0 AND length(sha256)=64 AND
  ((mime IN ('image/png','image/jpeg') AND width>0 AND height>0) OR
   (mime NOT IN ('image/png','image/jpeg') AND width=0 AND height=0))
 )
);
